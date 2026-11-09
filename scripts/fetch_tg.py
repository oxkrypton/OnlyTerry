#!/usr/bin/env python3
"""把目标群聊全量同步为本地「事实源」，不关心展示逻辑。

只做四件事：
  1. 读 data/state.json 的游标 last_msg_id（= 全群已同步的最大消息 ID）
  2. 拉取游标之后、指定话题内的**所有成员**消息（不做 from_user 过滤）
  3. 可选回补最近 N 条的编辑 / 删除
  4. 写 data/raw/YYYY-MM.json + data/state.json

展示规则一律放在 scripts/build_view.py，改展示不需要重新抓历史。
话题只由 .env 的 TARGET_TOPIC 指定（0 = 全群）；媒体默认不下载，只记元数据。
不按发送者做任何筛选：所有成员的消息与媒体一视同仁；目标用户只用于写 state 供展示层过滤。

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

RAW_DIR = Path("data/raw")
MEDIA_DIR = Path("media")
STATE_FILE = Path("data/state.json")

LOCAL_TZ = ZoneInfo(os.getenv("TZ_LOCAL", "Asia/Shanghai"))
# none | all；不按发送者筛选。默认 none = 只记媒体元数据，不下载文件
DOWNLOAD_MEDIA = os.getenv("DOWNLOAD_MEDIA", "none").lower()
# 论坛话题 ID，只从 .env 的 TARGET_TOPIC 读取；0 = 抓全群
TOPIC_ID = int(os.getenv("TARGET_TOPIC", "0") or 0)
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
        "reply_to_msg_id": getattr(reply, "reply_to_msg_id", None) if reply else None,
        "deleted": False,
    }


async def maybe_download(msg, kind):
    if kind not in ("photo", "video", "document"):
        return None
    # 全量：所有成员一视同仁，不按 sender_id 过滤
    if DOWNLOAD_MEDIA != "all":
        return None

    ext = ".jpg" if kind == "photo" else (getattr(getattr(msg, "file", None), "ext", None) or ".bin")
    if not str(ext).startswith("."):
        ext = "." + str(ext)
    MEDIA_DIR.mkdir(parents=True, exist_ok=True)
    dest = MEDIA_DIR / f"{msg.id}{ext}"
    if dest.exists():
        return f"media/{dest.name}"
    # FloodWaitError 故意不在这里吞：交给主循环中断本轮，游标保持不动
    saved = await msg.download_media(file=str(dest))
    return f"media/{Path(saved).name}" if saved else None


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
    print(f"[config] 目标群 {entity}，话题 {TOPIC_ID or '全部'}，媒体下载 {DOWNLOAD_MEDIA}")

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
                path = await maybe_download(msg, kind)
                records[msg.id] = make_record(msg, kind, path)
                added += 1
            completed = True
        except FloodWaitError as e:
            # 中途限流：不推进游标，下轮重跑（写入幂等），避免漏掉未处理的老消息
            print(f"[fetch] FloodWait {e.seconds}s，本轮到此为止，游标不推进")

        changed = await refresh_recent(client, entity, records)

    new_cursor = seen_max if completed else last_id
    prev = load_json(STATE_FILE, {})
    # 无新增、无回补、游标与统计都没变 -> 完全不落盘，避免每 20 分钟一次空 commit / 空部署
    no_change = (
        added == 0 and changed == 0
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
        f"新增 {added} 条，回补 {changed} 条，总计 {len(records)} 条，"
        f"游标 {last_id} -> {new_cursor}，月份 {sorted(groups, reverse=True)}"
    )


if __name__ == "__main__":
    asyncio.run(main())
