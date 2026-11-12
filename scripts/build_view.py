#!/usr/bin/env python3
"""展示投影：从 data/session*/raw 事实源派生 dist/，不连接 Telegram。

当前规则：合并所有话题 session，只展示目标用户的发言。
以后要加「回复上下文 / 话题聚合 / 关键词筛选」只改本文件，然后重跑即可重放全部历史，
不需要重新抓取。通过 INCLUDE_CONTEXT=0 可关闭回复上下文（默认开启）。

回复判定：论坛话题里「直接发在话题里」的消息，reply_to_msg_id 等于话题根消息 ID
（= TARGET_TOPIC，例如 session2 里的 2），它只是话题归属，不是回复某人；
只有指向另一条具体消息的才算引用回复。找得到原文的展示原文，找不到的输出轻量占位。
媒体路径在 raw 里是相对仓库根的完整路径（data/<session>/media/<msg_id>.jpg），
投影时只取文件名扁平拷进 dist/media/；同群消息 ID 全局唯一，跨 session 不会撞名。

i18n 说明：语言包 i18n.json 只负责**界面文案**。消息正文 / 回复上下文原文保持 Telegram
原文，不做简繁转换、不做翻译，避免污染事实源。
"""
from __future__ import annotations

import json
import os
import re
import shutil
from datetime import datetime, timezone
from pathlib import Path

DATA_ROOT = Path("data")
# 前端源码统一放在 frontend/；本脚本只把它们复制进 dist/ 发布
FRONTEND = Path("frontend")
HTML_FILE = FRONTEND / "index.html"
I18N_FILE = FRONTEND / "i18n.json"
AVATAR_FILE = FRONTEND / "avatar.webp"  # 气泡头像（96x96 WebP）
# index.html 以相对路径引用这些静态资源；dist/ 不进 git，必须在这里复制，
# 否则 GitHub Pages 产物会缺 CSS/JS 导致 404。新增前端文件记得加进这里。
ASSET_FILES = ("style.css", "app.js", "glass.js")
DIST = Path("dist")
DIST_DATA = DIST / "data"
DIST_MEDIA = DIST / "media"

DISPLAY_NAME = os.getenv("DISPLAY_NAME", "Terry")
INCLUDE_CONTEXT = os.getenv("INCLUDE_CONTEXT", "1") == "1"
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


def topic_id_of(session_dir, state):
    """话题根消息 ID = TARGET_TOPIC。新 state.json 会显式记录 topic_id；
    历史 state 没有该字段时从目录名 session<N> 回落解析。"""
    tid = state.get("topic_id")
    if isinstance(tid, int) and tid > 0:
        return tid
    m = re.fullmatch(r"session(\d+)", session_dir.name)
    n = int(m.group(1)) if m else 0
    return n or None


def with_context(post, by_id, topic_id, uid):
    """把被引用（被回复）的原消息挂到展示条目上，原文保持 Telegram 原样。

    输出字段：text 原文 / quote 博主选中的引用片段（仅新抓取的数据有）/
    date / media_type / is_self / sender / missing。找不到原文时不丢弃，
    给前端一个占位，读者仍能看出「这条是回复」。
    """
    rid = post.get("reply_to_msg_id")
    if not rid:
        return post
    # 话题根 ID 只是话题归属，不是引用；带 quote 片段的极少数例外仍按回复处理
    if topic_id and rid == topic_id and not post.get("reply_quote"):
        return post

    out = dict(post)
    ctx = by_id.get(rid)
    if not ctx or ctx.get("deleted"):
        out["reply_context"] = {"msg_id": rid, "missing": True}
        return out

    out["reply_context"] = {
        "msg_id": ctx["id"],
        "text": ctx.get("text", ""),  # 原文，不做简繁转换 / 翻译
        "quote": post.get("reply_quote") or None,  # 博主回复时选中的片段
        "date": ctx.get("date_local"),
        "media_type": ctx.get("media_type"),  # 无文本时给前端显示「图片 / 媒体内容」
        "is_self": ctx.get("sender_id") == uid,
        # 匿名时不给 ID，由前端按当前语言显示「群友 / Group member」
        "sender": None if ANONYMIZE_CONTEXT else ctx.get("sender_id"),
    }
    return out


def main():
    cfg = validate_i18n(load_json(I18N_FILE, None))

    sessions = sorted(p for p in DATA_ROOT.glob("session*") if (p / "raw").is_dir())
    uids = set()
    rows = []
    topic_by_session = {}
    for session_dir in sessions:
        st = load_json(session_dir / "state.json", {})
        if st.get("target_user_id"):
            uids.add(st["target_user_id"])
        topic_by_session[session_dir.name] = topic_id_of(session_dir, st)
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
        posts = [
            with_context(p, by_id, topic_by_session.get(p.get("session")), uid)
            for p in posts
        ]

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
    shutil.copy2(HTML_FILE, DIST / "index.html")
    for name in ASSET_FILES:
        shutil.copy2(FRONTEND / name, DIST / name)
    if AVATAR_FILE.exists():
        shutil.copy2(AVATAR_FILE, DIST / AVATAR_FILE.name)
    quoted = sum(1 for p in posts if p.get("reply_context"))
    print(
        f"投影完成：{len(posts)} 条展示条目，其中引用回复 {quoted} 条，"
        f"{len(sessions)} 个 session，"
        f"{len({r['month'] for r in rows})} 个月份，语言 {', '.join(cfg['locales'])}"
    )


if __name__ == "__main__":
    main()
