#!/usr/bin/env python3
"""把目标群聊全量同步为本地「事实源」，不关心展示逻辑。

只做四件事：
  1. 读 data/<session>/state.json 的游标 last_msg_id（= 全群已同步的最大消息 ID）
  2. 拉取游标之后、指定话题内的**所有成员**消息（不做 from_user 过滤）
  3. 可选回补最近 N 条的编辑 / 删除；补下历史里目标用户缺的图
  4. 写 data/<session>/raw/YYYY-MM.json + data/<session>/state.json

展示规则一律放在 scripts/build_view.py，改展示不需要重新抓历史。
一个话题一个 session（data/session2/、data/session3/…），各自独立游标：
新话题本地先跑一次全量，之后 CI 只做增量。话题由 TARGET_TOPIC 指定（0 = 全群）。
消息仍全量抓取以保留上下文；媒体只下载**目标用户**发的图片，和 raw 同放在
data/<session>/ 下，不新增仓库顶层目录。图片随 data/ 一起提交。

配置来源：
  - 本地：仓库根 .env（或环境变量 TG_ENV_FILE 指定路径），参考 .env.example
  - GitHub Actions：GITHUB_ACTIONS=true 时完全跳过 .env，只读 Secrets 注入的环境变量
  - 环境变量优先级始终高于 .env（setdefault，不覆盖已有值）
"""
from __future__ import annotations

import asyncio
import json
import os
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path
from zoneinfo import ZoneInfo

from telethon import TelegramClient, utils
from telethon.errors import FloodWaitError
from telethon.sessions import StringSession
from telethon.tl.types import MessageEmpty

REQUIRED_ENV = ("TG_API_ID", "TG_API_HASH", "TG_SESSION", "TARGET_GROUP", "TARGET_USER")


def parse_dotenv(text):
    """极简 .env 解析：KEY=VALUE、# 注释、export 前缀、单/双引号；不做变量插值。"""
    out = {}
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        if "=" not in line:
            continue
        key, _, value = line.partition("=")
        key, value = key.strip(), value.strip()
        if not key:
            continue
        if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
            value = value[1:-1]
        else:
            value = value.split(" #", 1)[0].rstrip()
        out[key] = value
    return out


def load_local_dotenv():
    """CI 只读 Secrets；本地把 .env 补进未设置的环境变量（已有值优先）。"""
    if os.environ.get("GITHUB_ACTIONS") == "true":
        print("[config] GitHub Actions：跳过 .env，仅使用 Secrets 注入的环境变量")
        return
    candidates = []
    if os.environ.get("TG_ENV_FILE"):
        candidates.append(Path(os.environ["TG_ENV_FILE"]).expanduser())
    candidates.append(Path.cwd() / ".env")
    candidates.append(Path(__file__).resolve().parent.parent / ".env")
    for path in candidates:
        if not path.is_file():
            continue
        for key, value in parse_dotenv(path.read_text(encoding="utf-8")).items():
            os.environ.setdefault(key, value)
        print(f"[config] 已加载 {path}")
        return
    print("[config] 未找到 .env，使用环境变量")


load_local_dotenv()

missing = [k for k in REQUIRED_ENV if not os.environ.get(k)]
if missing:
    raise SystemExit(
        "缺少配置：" + ", ".join(missing) +
        "\n本地请在仓库根 .env 填写（参考 .env.example），或用 TG_ENV_FILE 指定路径；"
        "\nCI 请在仓库 Settings → Secrets and variables → Actions 中配置。"
    )

API_ID = int(os.environ["TG_API_ID"])
API_HASH = os.environ["TG_API_HASH"]
SESSION_STR = os.environ["TG_SESSION"]
TARGET_GROUP = os.environ["TARGET_GROUP"]
TARGET_USER = os.environ["TARGET_USER"]

# 论坛话题 ID；0 = 抓全群
TOPIC_ID = int(os.getenv("TARGET_TOPIC", "0") or 0)
# 一个话题一个目录，各自独立游标；可用 SESSION_ID 覆盖目录名
SESSION_ID = os.getenv("SESSION_ID") or (f"session{TOPIC_ID}" if TOPIC_ID else "sessionAll")
SESSION_DIR = Path("data") / SESSION_ID
RAW_DIR = SESSION_DIR / "raw"
MEDIA_DIR = SESSION_DIR / "media"
STATE_FILE = SESSION_DIR / "state.json"

LOCAL_TZ = ZoneInfo(os.getenv("TZ_LOCAL", "Asia/Shanghai"))
# none   = 不下图，只记 media_type 元数据
# target = 只下目标用户发的图片（默认）
# all    = 下所有人的图片
# 三档都只处理 photo；视频 / 文档 / 贴纸一律只记元数据
DOWNLOAD_MEDIA = os.getenv("DOWNLOAD_MEDIA", "target").lower()
# 每次批量回补最近 N 条，捕获编辑 / 删除；0 = 关闭
REFRESH_RECENT = int(os.getenv("REFRESH_RECENT", "200"))
WAIT_TIME = float(os.getenv("WAIT_TIME", "1"))


def load_json(path: Path, default):
    if path.exists():
        with path.open("r", encoding="utf-8") as f:
            return json.load(f)
    return default


def save_json(path: Path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    with tmp.open("w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
    tmp.replace(path)


def parse_entity(val: str):
    val = val.strip()
    return int(val) if val.lstrip("-").isdigit() else val


def topic_of(msg):
    """话题内回复看 reply_to_top_id，直接发在话题里看 reply_to_msg_id。"""
    r = getattr(msg, "reply_to", None)
    if r is None:
        return None
    return getattr(r, "reply_to_top_id", None) or getattr(r, "reply_to_msg_id", None)


def month_of(dt: datetime) -> str:
    # Telethon 的 date 是 UTC；按本地时区分片，避免北京时间月初 8 小时错档
    return dt.astimezone(LOCAL_TZ).strftime("%Y-%m")


def media_kind(msg):
    if getattr(msg, "photo", None):
        return "photo"
    if getattr(msg, "video", None):
        return "video"
    if getattr(msg, "sticker", None):
        return "sticker"
    if getattr(msg, "document", None):
        return "document"
    return None


def make_record(msg, kind, media_path):
    d = msg.date
    reply = getattr(msg, "reply_to", None)
    return {
        "id": msg.id,
        "date": d.isoformat(),
        "date_local": d.astimezone(LOCAL_TZ).isoformat(),
        "month": month_of(d),
        "sender_id": msg.sender_id,
        "text": msg.message or "",  # 图片/文档的 caption 也在这里
        "media": media_path,
        "media_type": kind,
        "grouped_id": getattr(msg, "grouped_id", None),  # 相册分组，展示层目前不聚合
        "reply_to_msg_id": getattr(reply, "reply_to_msg_id", None) if reply else None,
        "deleted": False,
    }


async def maybe_download(msg, kind, target_uid):
    """返回相对仓库根的图片路径；只处理 photo，target 模式只下目标用户。"""
    if kind != "photo" or DOWNLOAD_MEDIA == "none":
        return None
    if DOWNLOAD_MEDIA == "target" and msg.sender_id != target_uid:
        return None

    ext = getattr(getattr(msg, "file", None), "ext", None) or ".jpg"
    if not str(ext).startswith("."):
        ext = "." + str(ext)
    MEDIA_DIR.mkdir(parents=True, exist_ok=True)
    dest = MEDIA_DIR / f"{msg.id}{ext}"
    if dest.exists():
        return dest.as_posix()
    # FloodWaitError 故意不在这里吞：交给主循环中断本轮，游标保持不动
    saved = await msg.download_media(file=str(dest))
    return dest.as_posix() if saved else None


async def backfill_media(client, entity, records, target_uid):
    """补下历史里缺的图：raw 只存元数据，按消息 ID 批量取回原消息再下载。

    已下载的 media 非空 -> 下轮自动跳过，天然断点续传；首次跑一次性补完
    全部历史图片（几百张），之后每轮只有新增的几张。FloodWait 中断时本轮
    已补的照常落盘，下一轮继续，不阻塞消息游标。
    """
    if DOWNLOAD_MEDIA == "none":
        return 0

    def pending(r):
        return (
            r.get("media_type") == "photo"
            and not r.get("media")
            and not r.get("deleted")
            and (DOWNLOAD_MEDIA == "all" or r.get("sender_id") == target_uid)
        )

    missing = sorted(mid for mid, r in records.items() if pending(r))
    if not missing:
        return 0
    print(f"[media] 待补 {len(missing)} 张（{SESSION_ID}）")
    done = 0
    for i in range(0, len(missing), 100):
        chunk = missing[i:i + 100]
        try:
            fresh = await client.get_messages(entity, ids=chunk)
        except FloodWaitError as e:
            print(f"[media] FloodWait {e.seconds}s，本轮补图到此为止")
            break
        for m in fresh:
            if m is None or isinstance(m, MessageEmpty) or not getattr(m, "photo", None):
                continue
            if m.id not in records:
                continue
            try:
                # pending 已按 records 的 sender_id 过滤，这里放宽到消息自身，避免重复重试
                path = await maybe_download(m, "photo", m.sender_id)
            except FloodWaitError as e:
                print(f"[media] FloodWait {e.seconds}s，本轮补图到此为止")
                return done
            if path:
                records[m.id]["media"] = path
                done += 1
    return done


def load_all_raw():
    records = {}
    for p in sorted(RAW_DIR.glob("*.json")):
        for r in load_json(p, []):
            records[int(r["id"])] = r
    return records


async def refresh_recent(client, entity, records):
    """批量回补近期消息，捕获编辑与删除；保留原记录不物理删除。"""
    if REFRESH_RECENT <= 0 or not records:
        return 0
    ids = sorted(records)[-REFRESH_RECENT:]
    changed = 0
    for i in range(0, len(ids), 100):
        chunk = ids[i:i + 100]
        try:
            fresh = await client.get_messages(entity, ids=chunk)
        except FloodWaitError as e:
            print(f"[refresh] FloodWait {e.seconds}s，跳过剩余回补")
            break
        for mid, m in zip(chunk, fresh):
            old = records.get(mid)
            if not old:
                continue
            if m is None or isinstance(m, MessageEmpty):
                if not old.get("deleted"):
                    old["deleted"] = True
                    changed += 1
            else:
                text = m.message or ""
                if old.get("text") != text or old.get("deleted"):
                    old["text"] = text
                    old["deleted"] = False
                    changed += 1
    return changed


async def main():
    state = load_json(STATE_FILE, {})
    last_id = int(state.get("last_msg_id", 0))
    records = load_all_raw()
    print(f"游标 last_msg_id={last_id}，本地已有 {len(records)} 条")

    entity = parse_entity(TARGET_GROUP)
    print(
        f"[config] 目标群 {entity}，话题 {TOPIC_ID or '全部'}，"
        f"session {SESSION_DIR.as_posix()}，媒体 {DOWNLOAD_MEDIA}"
    )

    async with TelegramClient(StringSession(SESSION_STR), API_ID, API_HASH) as client:
        # 目标用户解析成数值 ID，写入 state 供 build_view 使用（展示层不连 TG）
        user = parse_entity(TARGET_USER)
        target_uid = user if isinstance(user, int) else utils.get_peer_id(
            await client.get_input_entity(user)
        )
        state["target_user_id"] = target_uid

        seen_max = last_id
        added = 0
        completed = False
        try:
            # 不用 from_user：避免走 SearchRequest（频道需管理员权限、min_id 只在客户端截断）
            # 不用 reply_to：GetRepliesRequest 的 min_id 恒为 0，会破坏增量游标，改在客户端按话题过滤
            # min_id 保证只处理游标之后的全群消息；默认 newest -> oldest，最后统一排序
            async for msg in client.iter_messages(entity, min_id=last_id, wait_time=WAIT_TIME):
                # 游标按全群推进：其它话题也计入，避免每轮重复扫描历史
                if msg.id > seen_max:
                    seen_max = msg.id
                if TOPIC_ID and topic_of(msg) != TOPIC_ID:
                    continue
                kind = media_kind(msg)
                if not (msg.message or kind):
                    continue  # 无文本无媒体的系统消息
                path = await maybe_download(msg, kind, target_uid)
                records[msg.id] = make_record(msg, kind, path)
                added += 1
            completed = True
        except FloodWaitError as e:
            # 中途限流：不推进游标，下轮重跑（写入幂等），避免漏掉未处理的老消息
            print(f"[fetch] FloodWait {e.seconds}s，本轮到此为止，游标不推进")

        changed = await refresh_recent(client, entity, records)
        media_added = await backfill_media(client, entity, records, target_uid)

    new_cursor = seen_max if completed else last_id
    prev = load_json(STATE_FILE, {})
    # 无新增、无回补、无补图、游标与统计都没变 -> 完全不落盘，避免空 commit / 空部署
    no_change = (
        added == 0 and changed == 0 and media_added == 0
        and prev.get("target_user_id") == target_uid
        and prev.get("last_msg_id") == new_cursor
        and prev.get("total") == len(records)
    )
    if no_change:
        print("无新增、无变更，跳过落盘（不产生空 commit）")
        return

    # 事实源按月全量重写；字段顺序固定 -> 内容不变时 git 无 diff
    groups = defaultdict(list)
    for r in records.values():
        groups[r["month"]].append(r)
    for month, rows in groups.items():
        rows.sort(key=lambda x: x["id"])
        save_json(RAW_DIR / f"{month}.json", rows)

    state["last_msg_id"] = new_cursor
    state["updated_at"] = datetime.now(timezone.utc).isoformat()
    state["total"] = len(records)
    save_json(STATE_FILE, state)
    print(
        f"新增 {added} 条，回补 {changed} 条，补图 {media_added} 张，总计 {len(records)} 条，"
        f"游标 {last_id} -> {new_cursor}，月份 {sorted(groups, reverse=True)}"
    )


if __name__ == "__main__":
    asyncio.run(main())
