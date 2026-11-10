#!/usr/bin/env python3
"""展示投影：从 data/session*/raw 事实源派生 dist/，不连接 Telegram。

当前规则：合并所有话题 session，只展示目标用户的发言。
以后要加「回复上下文 / 话题聚合 / 关键词筛选」只改本文件，然后重跑即可重放全部历史，
不需要重新抓取。通过 INCLUDE_CONTEXT=1 可直接开启上下文（默认关闭）。
媒体路径在 raw 里是相对仓库根的完整路径（data/<session>/media/<msg_id>.jpg），
投影时只取文件名扁平拷进 dist/media/；同群消息 ID 全局唯一，跨 session 不会撞名。

i18n 说明：语言包 i18n.json 只负责**界面文案**。消息正文 / 回复上下文原文保持 Telegram
原文，不做简繁转换、不做翻译，避免污染事实源。
"""
from __future__ import annotations

import json
import os
import shutil
from datetime import datetime, timezone
from pathlib import Path

DATA_ROOT = Path("data")
# 每个话题一个 session：data/session{N}/raw/*.json + data/session{N}/state.json
I18N_FILE = Path("i18n.json")
AVATAR_FILE = Path("avatar.webp")  # 前端气泡头像（96x96 WebP，构建时随站点一起发布）
DIST = Path("dist")
DIST_DATA = DIST / "data"
DIST_MEDIA = DIST / "media"

DISPLAY_NAME = os.getenv("DISPLAY_NAME", "Terry")
INCLUDE_CONTEXT = os.getenv("INCLUDE_CONTEXT", "0") == "1"
# 上下文默认不暴露群友 ID / 昵称
ANONYMIZE_CONTEXT = os.getenv("ANONYMIZE_CONTEXT", "1") == "1"

REQUIRED_LOCALES = ("zh-CN", "zh-TW", "en")
NAME_ENV = {
    "zh-CN": "DISPLAY_NAME_ZH_CN",
    "zh-TW": "DISPLAY_NAME_ZH_TW",
    "en": "DISPLAY_NAME_EN",
}


def load_json(path: Path, default):
    if path.exists():
        with path.open("r", encoding="utf-8") as f:
            return json.load(f)
    return default


def save_json(path: Path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)


def validate_i18n(cfg):
    """构建期校验三种语言的文案键完全一致，避免上线后缺翻译。"""
    if not isinstance(cfg, dict):
        raise SystemExit("i18n.json 格式错误：应为对象")
    locales = cfg.get("locales") or []
    for loc in REQUIRED_LOCALES:
        if loc not in locales:
            raise SystemExit(f"i18n.json 缺少语言 {loc}")
    strings = cfg.get("strings") or {}
    base = set((strings.get(REQUIRED_LOCALES[0]) or {}).keys())
    if not base:
        raise SystemExit("i18n.json 基准语言没有任何文案键")
    for loc in REQUIRED_LOCALES:
        keys = set((strings.get(loc) or {}).keys())
        missing, extra = base - keys, keys - base
        if missing or extra:
            raise SystemExit(
                f"i18n.json [{loc}] 文案键不一致：缺 {sorted(missing)}，多 {sorted(extra)}"
            )
    return cfg


def display_names():
    """站点名可按语言覆盖；未配置的回落到 DISPLAY_NAME。"""
    return {loc: (os.getenv(var) or DISPLAY_NAME) for loc, var in NAME_ENV.items()}


def with_context(post, by_id):
    """扩展点：把被回复的原始消息挂到展示条目上（原文保持原样）。"""
    rid = post.get("reply_to_msg_id")
    ctx = by_id.get(rid) if rid else None
    if not ctx or ctx.get("deleted"):
        return post
    out = dict(post)
    out["reply_context"] = {
        "msg_id": ctx["id"],
        "text": ctx.get("text", ""),  # 原文，不做简繁转换 / 翻译
        "date": ctx.get("date_local"),
        # 匿名时不给文案，由前端按当前语言显示「群友 / Group member」
        "sender": None if ANONYMIZE_CONTEXT else ctx.get("sender_id"),
    }
    return out


def main():
    cfg = validate_i18n(load_json(I18N_FILE, None))

    sessions = sorted(p for p in DATA_ROOT.glob("session*") if (p / "raw").is_dir())
    uids = set()
    rows = []
    for session_dir in sessions:
        st = load_json(session_dir / "state.json", {})
        if st.get("target_user_id"):
            uids.add(st["target_user_id"])
        for p in sorted((session_dir / "raw").glob("*.json")):
            for r in load_json(p, []):
                r = dict(r)
                r["session"] = session_dir.name
                rows.append(r)
    uid = next(iter(uids), None)
    # 同一群内 msg.id 全局唯一，跨 session 可直接合并；换群时需改用 (session, id)
    by_id = {r["id"]: r for r in rows}
    if not sessions:
        print("警告：data/ 下没有 session*/raw/，请先跑 make fetch")

    posts = [
        r for r in rows
        if not r.get("deleted") and (not uids or r.get("sender_id") in uids)
    ]
    posts.sort(key=lambda r: r["id"], reverse=True)
    if INCLUDE_CONTEXT:
        posts = [with_context(p, by_id) for p in posts]

    # 只把展示真正用到的媒体拷进站点产物，非目标用户的图片不对外发布
    # rel = data/<session>/media/<msg_id>.jpg，取 basename 扁平放入 dist/media/
    DIST_MEDIA.mkdir(parents=True, exist_ok=True)
    for post in posts:
        rel = post.get("media")
        if rel and Path(rel).exists():
            shutil.copy2(rel, DIST_MEDIA / Path(rel).name)

    save_json(DIST_DATA / "posts.json", {
        "display_name": DISPLAY_NAME,          # 兼容旧字段
        "display_names": display_names(),      # 按语言取站点名
        "target_user_id": uid,
        "updated_at": datetime.now(timezone.utc).isoformat(),
        "count": len(posts),
        "posts": posts,
    })
    save_json(DIST_DATA / "months.json", {
        "months": sorted({r["month"] for r in rows}, reverse=True),
    })
    shutil.copy2(I18N_FILE, DIST / "i18n.json")
    shutil.copy2("index.html", DIST / "index.html")
    if AVATAR_FILE.exists():
        shutil.copy2(AVATAR_FILE, DIST / AVATAR_FILE.name)
    print(
        f"投影完成：{len(posts)} 条展示条目，{len(sessions)} 个 session，"
        f"{len({r['month'] for r in rows})} 个月份，语言 {', '.join(cfg['locales'])}"
    )


if __name__ == "__main__":
    main()
