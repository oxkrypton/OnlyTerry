#!/usr/bin/env python3
"""生成演示用的 mock 事实源 + 可交互预览。不连接 Telegram、不修改前端源码。

与真实链路的关系：
  - 真实事实源在 data/raw/，由 scripts/fetch_tg.py 写入；本脚本**绝不碰** data/。
  - mock 事实源写在 mock/（data/raw 的同构副本），字段与 fetch_tg.make_record 完全一致。
  - 预览仍走官方投影 scripts/build_view.py：raw -> dist/data/posts.json，
    所以改前端 UI 时看到的就是真实数据形状，不是另写的一套假结构。
  - dist/index.html 额外内联注入 i18n / posts，仅为让 file:// 离线预览可点；
    生产（GitHub Pages）依旧走相对路径 fetch，注入逻辑由前端 loadJson 自动跳过。

用法：
  python3 scripts/mock_data.py                # 生成 mock/ 并重建 dist/ 预览
  python3 scripts/mock_data.py --no-context   # 不带回复上下文（对齐 workflow 默认 INCLUDE_CONTEXT=0）

覆盖的边界场景：跨月分片（UTC 月末 -> 北京次月）、多发送者、简/繁/英原文、
长文本与换行、emoji 与链接、纯图无 caption、图片 + caption、CSV/PDF 附件、
回复上下文（含空文本原文）、已编辑、已删除、群友图片不进站点产物。
"""
from __future__ import annotations

import argparse
import json
import os
import random
import shutil
import struct
import subprocess
import sys
import tempfile
import zlib
from datetime import datetime, timezone
from pathlib import Path
from zoneinfo import ZoneInfo

ROOT = Path(__file__).resolve().parents[1]
MOCK = ROOT / "mock"
DIST = ROOT / "dist"
TZ_LOCAL = ZoneInfo("Asia/Shanghai")

TRADER = 582910293  # 目标用户：博主
LIN = 77100231
MAGGIE = 77100456
ZHOU = 77100999
KEN = 77101234
BOT = 90010001

JPEG_IDS = (12014, 12027)  # 这两张尝试转成真 JPEG，验证前端 jpg 分支


# --------------------------------------------------------------------------
# 时间工具：Telethon 的 msg.date 是 UTC；记录里的 month 按 Asia/Shanghai 分片
# --------------------------------------------------------------------------
def bj(s: str) -> datetime:
    return datetime.strptime(s, "%Y-%m-%d %H:%M").replace(tzinfo=TZ_LOCAL).astimezone(timezone.utc)


def utc(s: str) -> datetime:
    return datetime.strptime(s, "%Y-%m-%d %H:%M").replace(tzinfo=timezone.utc)


def record(mid, when, sid, text, reply=None, media=None, kind=None, deleted=False):
    local = when.astimezone(TZ_LOCAL)
    return {
        "id": mid,
        "date": when.isoformat(),
        "date_local": local.isoformat(),
        "month": local.strftime("%Y-%m"),
        "sender_id": sid,
        "text": text,
        "media": media,
        "media_type": kind,
        "reply_to_msg_id": reply,
        "deleted": deleted,
    }


def messages(jpeg_ok: bool):
    def ext(mid):
        return ".jpg" if (mid in JPEG_IDS and jpeg_ok) else ".png"

    return [
        # ---------- 2026-09 ----------
        record(12001, bj("2026-09-03 09:12"), ZHOU,
               "阿哲，早。今天开盘前能聊聊大盘节奏吗？"),
        record(12002, bj("2026-09-03 09:20"), TRADER,
               "早。今天先看量能，指数在区间上沿，别急着追。\n\n"
               "三个观察点：\n"
               "1. 开盘 30 分钟成交额\n"
               "2. 资金方向\n"
               "3. 昨日高点是否守住", reply=12001),
        record(12003, bj("2026-09-08 20:15"), TRADER,
               "【9 月第一周复盘】\n"
               "这一周没有做加法，只做了减法：把两笔逻辑重叠的仓位合并，"
               "把一笔靠情绪进场的单子平掉。\n\n"
               "复盘最容易犯的错，是把结果当成过程的证据。赚了不代表当时判断对，"
               "亏了也不代表纪律错了。只看一件事：当时有没有按计划执行。\n\n"
               "下周继续等结构走清楚，不预设方向。"),
        record(12004, bj("2026-09-08 20:31"), LIN,
               "老师，区间上沿大概看哪个位置？"),
        record(12005, bj("2026-09-08 20:33"), TRADER,
               "不谈具体点位哈，看结构就行：放量突破且回踩不破，才算有效。", reply=12004),
        record(12006, bj("2026-09-18 14:02"), MAGGIE,
               "有最新的持仓明细吗？想对着表格看一遍。"),
        record(12007, bj("2026-09-18 14:10"), TRADER,
               "周末整理一版发出来。先说结论：仓位比方向重要。", reply=12006),

        # ---------- 跨月边界：UTC 9/30 16:30 == 北京 10/1 00:30 ----------
        record(12008, utc("2026-09-30 16:30"), TRADER,
               "深夜补一句：这条按 UTC 属于 9 月，按归档时区应归到 10 月，"
               "用来验证按月分片没错档。🌙"),

        # ---------- 2026-10 ----------
        record(12009, bj("2026-10-05 10:02"), MAGGIE,
               "Any update on the USD leg? Thanks!"),
        record(12010, bj("2026-10-05 10:08"), TRADER,
               "Still watching. No change to the plan until the weekly close. "
               "Risk first, always.", reply=12009),
        record(12011, bj("2026-10-09 15:20"), TRADER,
               "日内结构图，收盘后补一张。",
               media="media/12011.png", kind="photo"),
        record(12012, bj("2026-10-09 15:32"), KEN,
               "这张图能讲讲右侧那一段吗？"),
        record(12013, bj("2026-10-09 15:40"), TRADER,
               "顺手把明细导出来了，CSV 自取。",
               reply=12012, media="media/12013.csv", kind="document"),
        record(12014, bj("2026-10-12 09:05"), TRADER, "",
               media="media/12014" + ext(12014), kind="photo"),  # 纯图无 caption
        record(12015, bj("2026-10-15 11:22"), ZHOU,
               "想請問一下，倉位要怎麼控管？"),
        record(12016, bj("2026-10-15 11:30"), TRADER,
               "風險控管優先，單筆虧損先設上限，其餘交給紀律。", reply=12015),
        record(12017, bj("2026-10-20 21:11"), TRADER,
               "（此條已撤回）", deleted=True),
        record(12018, bj("2026-10-21 08:55"), LIN,
               "早上好，今天有什麼要注意的？"),
        record(12019, bj("2026-10-21 09:01"), TRADER,
               "早 ☕\n今天只有兩件事：\n"
               "① 等第一波波動走完再動手\n② 不因為錯過而追價\n\n"
               "筆記：https://example.com/notes/2026-10", reply=12018),
        record(12020, bj("2026-10-22 07:30"), BOT,
               "群公告已更新：發言請附上時間與依據。"),
        record(12021, bj("2026-10-25 19:40"), TRADER,
               "【10 月小結】\n"
               "整月執行了 6 次計畫內操作，1 次計畫外操作。計畫外那次是唯一的虧損來源，"
               "金額不大，但性質最差。\n\n"
               "下個月把「進場前寫下理由」做成硬規則，寫不出來就不進場。"),

        # ---------- 2026-11 ----------
        record(12022, bj("2026-11-02 09:10"), KEN,
               "周末消息面怎么解读？"),
        record(12023, bj("2026-11-02 09:18"), TRADER,
               "别过度解读单条消息，看它是否真的改变了供需结构。大多数时候没有。",
               reply=12022),
        record(12024, bj("2026-11-03 15:25"), TRADER,
               "本周交割单（演示图，非真实数据）。",
               media="media/12024.png", kind="photo"),
        record(12025, bj("2026-11-03 15:41"), MAGGIE,
               "这个盈亏比怎么算的？"),
        record(12026, bj("2026-11-03 15:48"), TRADER,
               "用实际成交价算，不含手续费和滑点的话会偏乐观一些。", reply=12025),
        record(12027, bj("2026-11-05 20:12"), TRADER, "",
               media="media/12027" + ext(12027), kind="photo"),
        record(12028, bj("2026-11-06 10:03"), MAGGIE,
               "Could you share the risk checklist again?"),
        record(12029, bj("2026-11-06 10:11"), TRADER,
               "Sure — sizing, stop, correlation, liquidity. In that order.", reply=12028),
        record(12030, bj("2026-11-07 16:44"), TRADER,
               "周报 PDF（演示附件）。", media="media/12030.pdf", kind="document"),
        record(12031, bj("2026-11-07 17:02"), ZHOU, "收到，谢谢！"),
        record(12032, bj("2026-11-08 21:30"), TRADER,
               "【风险提示 · 置顶一下】\n"
               "归档里的所有内容只是我自己的记录，不是任何形式的建议。\n"
               "市场有风险，任何数字都不要直接照搬。\n\n"
               "另外提醒：截图里包含模拟账户信息，请勿当作真实业绩。\n"
               "规则说明：https://example.com/disclaimer"),
        record(12033, bj("2026-11-09 08:52"), LIN,
               "今天盤前有任何提醒嗎？"),
        record(12034, bj("2026-11-09 08:58"), TRADER,
               "盤前不預設方向，等第一根 K 線收完再說。", reply=12033),
        record(12035, bj("2026-11-09 13:20"), TRADER,
               "【演示数据】这条用于检查长文本、emoji 📌 与换行渲染。\n"
               "第二行：中文简体保持原样。\n"
               "第三行：繁體、English、123.45% 混排也不做任何转换。\n"
               "第四行：末尾用来观察卡片在窄屏下的换行表现，"
               "所以故意写得稍长一点，看看会不会撑破容器。"),
        record(12036, bj("2026-11-09 13:35"), MAGGIE,
               "上面那张图我看不清，能重发吗？"),
        record(12037, bj("2026-11-09 13:40"), MAGGIE, "",
               media="media/12037.png", kind="photo"),  # 群友图片：不该进 dist/media
        record(12038, bj("2026-11-09 13:44"), TRADER,
               "我这边看是清晰的，稍后再补一张文字版要点。", reply=12037),
        record(12039, bj("2026-11-09 14:02"), TRADER,
               "（重复的草稿，已删除）", deleted=True),
        record(12040, bj("2026-11-09 14:30"), TRADER,
               "收工。今天没有新增操作，保持原计划。✅"),
    ]


# --------------------------------------------------------------------------
# 极简 PNG 编码器（零第三方依赖）：8-bit RGB，逐行 filter 0
# --------------------------------------------------------------------------
GLYPHS = {
    "0": ("01110", "10001", "10011", "10101", "11001", "10001", "01110"),
    "1": ("00100", "01100", "00100", "00100", "00100", "00100", "01110"),
    "2": ("01110", "10001", "00001", "00010", "00100", "01000", "11111"),
    "3": ("11110", "00001", "00001", "01110", "00001", "00001", "11110"),
    "4": ("00010", "00110", "01010", "10010", "11111", "00010", "00010"),
    "5": ("11111", "10000", "11110", "00001", "00001", "10001", "01110"),
    "6": ("00110", "01000", "10000", "11110", "10001", "10001", "01110"),
    "7": ("11111", "00001", "00010", "00100", "01000", "01000", "01000"),
    "8": ("01110", "10001", "10001", "01110", "10001", "10001", "01110"),
    "9": ("01110", "10001", "10001", "01111", "00001", "00010", "01100"),
    " ": ("00000",) * 7,
    ".": ("00000", "00000", "00000", "00000", "00000", "01100", "01100"),
    ",": ("00000", "00000", "00000", "00000", "01100", "01100", "00100"),
    ":": ("00000", "01100", "01100", "00000", "01100", "01100", "00000"),
    "%": ("11001", "11010", "00010", "00100", "01000", "01011", "10011"),
    "+": ("00000", "00100", "00100", "11111", "00100", "00100", "00000"),
    "-": ("00000", "00000", "00000", "11111", "00000", "00000", "00000"),
    "/": ("00001", "00010", "00010", "00100", "01000", "01000", "10000"),
    "#": ("01010", "01010", "11111", "01010", "11111", "01010", "01010"),
    "(": ("00010", "00100", "01000", "01000", "01000", "00100", "00010"),
    ")": ("01000", "00100", "00010", "00010", "00010", "00100", "01000"),
    "[": ("01110", "01000", "01000", "01000", "01000", "01000", "01110"),
    "]": ("01110", "00010", "00010", "00010", "00010", "00010", "01110"),
    "=": ("00000", "00000", "11111", "00000", "11111", "00000", "00000"),
    ">": ("01000", "00100", "00010", "00001", "00010", "00100", "01000"),
    "*": ("00000", "10101", "01110", "11111", "01110", "10101", "00000"),
}


class Canvas:
    def __init__(self, w, h, bg):
        self.w, self.h = w, h
        row = bytes(bg) * w
        self.buf = bytearray()
        for _ in range(h):
            self.buf.append(0)
            self.buf.extend(row)

    def px(self, x, y, c):
        if 0 <= x < self.w and 0 <= y < self.h:
            i = y * (1 + self.w * 3) + 1 + x * 3
            self.buf[i:i + 3] = bytes(c)

    def rect(self, x0, y0, x1, y1, c, fill=False):
        x0, x1 = sorted((int(x0), int(x1)))
        y0, y1 = sorted((int(y0), int(y1)))
        if fill:
            for y in range(y0, y1 + 1):
                for x in range(x0, x1 + 1):
                    self.px(x, y, c)
        else:
            for x in range(x0, x1 + 1):
                self.px(x, y0, c); self.px(x, y1, c)
            for y in range(y0, y1 + 1):
                self.px(x0, y, c); self.px(x1, y, c)

    def line(self, x0, y0, x1, y1, c, width=1):
        x0, y0, x1, y1 = int(x0), int(y0), int(x1), int(y1)
        dx, dy = abs(x1 - x0), abs(y1 - y0)
        sx, sy = (1 if x0 < x1 else -1), (1 if y0 < y1 else -1)
        err = dx - dy
        while True:
            self.rect(x0 - width // 2, y0 - width // 2,
                      x0 + width - 1 - width // 2, y0 + width - 1 - width // 2, c, True)
            if x0 == x1 and y0 == y1:
                break
            e2 = 2 * err
            if e2 > -dy:
                err -= dy; x0 += sx
            if e2 < dx:
                err += dx; y0 += sy

    def text(self, x, y, s, c, scale=2):
        for ch in str(s).upper():
            g = GLYPHS.get(ch, GLYPHS[" "])
            for ry, row in enumerate(g):
                for rx, bit in enumerate(row):
                    if bit == "1":
                        self.rect(x + rx * scale, y + ry * scale,
                                  x + (rx + 1) * scale - 1, y + (ry + 1) * scale - 1, c, True)
            x += 6 * scale


def write_png(path: Path, c: Canvas):
    def chunk(tag, data):
        return (struct.pack(">I", len(data)) + tag + data +
                struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF))

    ihdr = struct.pack(">IIBBBBB", c.w, c.h, 8, 2, 0, 0, 0)
    blob = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) +
            chunk(b"IDAT", zlib.compress(bytes(c.buf), 9)) + chunk(b"IEND", b""))
    path.write_bytes(blob)


def candle_chart(path: Path, dark: bool, seed: int, label: str):
    """模拟行情截图：红涨绿跌（A 股习惯），含网格 / 均线 / 量柱 / 数字刻度。"""
    rng = random.Random(seed)
    w, h = 720, 400
    bg = (15, 23, 33) if dark else (255, 255, 255)
    grid = (34, 48, 63) if dark else (231, 236, 242)
    fg = (200, 212, 226) if dark else (90, 100, 112)
    up, down = (232, 82, 77), (28, 158, 102)
    c = Canvas(w, h, bg)
    left, right, top, bottom = 54, 700, 46, 292
    for i in range(6):
        y = top + i * (bottom - top) // 5
        c.line(left, y, right, y, grid)
    for i in range(9):
        x = left + i * (right - left) // 8
        c.line(x, top, x, bottom, grid)
    c.rect(left, top, right, bottom, grid)
    for i, v in enumerate(("108.50", "106.20", "103.90", "101.60", "99.30", "97.00")):
        c.text(4, top + i * (bottom - top) // 5 - 6, v, fg, 1)

    n, step = 46, (right - left - 16) // 46
    closes, price = [], 102.0
    for i in range(n):
        o = price
        price = max(97.5, min(108.0, price + rng.uniform(-1.25, 1.35)))
        cl = price
        hi = max(o, cl) + rng.uniform(0.05, 0.75)
        lo = min(o, cl) - rng.uniform(0.05, 0.75)
        closes.append(cl)
        x = left + 8 + i * step
        def py(v):
            return bottom - (v - 97.0) / 11.5 * (bottom - top)
        col = up if cl >= o else down
        c.line(x, py(hi), x, py(lo), col, 2)
        c.rect(x - 3, py(max(o, cl)), x + 3, py(min(o, cl)), col, True)
        vol = 12 + abs(cl - o) * 22 + rng.uniform(0, 14)
        c.rect(x - 3, bottom + 40 - vol, x + 3, bottom + 40, col, True)
    ma = []
    for i in range(n):
        window = closes[max(0, i - 4):i + 1]
        ma.append(sum(window) / len(window))
    for i in range(1, n):
        def py(v):
            return bottom - (v - 97.0) / 11.5 * (bottom - top)
        c.line(left + 8 + (i - 1) * step, py(ma[i - 1]),
               left + 8 + i * step, py(ma[i]), (240, 185, 80), 2)
    c.line(left, bottom + 44, right, bottom + 44, grid)
    c.text(20, 14, label, fg, 2)
    c.text(560, 14, "+2.34%", up, 2)
    c.text(20, bottom + 56, "2026-11-09", fg, 1)
    c.text(330, bottom + 56, "60", fg, 1)
    c.text(560, bottom + 56, "104820.50", fg, 1)
    write_png(path, c)


def equity_chart(path: Path):
    """浅色净值曲线，用于「纯图无 caption」的展示卡片。"""
    rng = random.Random(77)
    w, h = 720, 360
    c = Canvas(w, h, (255, 255, 255))
    grid, fg = (232, 237, 243), (96, 106, 118)
    left, right, top, bottom = 56, 690, 44, 300
    for i in range(6):
        y = top + i * (bottom - top) // 5
        c.line(left, y, right, y, grid)
        c.text(8, y - 6, ("1.30", "1.24", "1.18", "1.12", "1.06", "1.00")[i], fg, 1)
    for i in range(7):
        x = left + i * (right - left) // 6
        c.line(x, top, x, bottom, grid)
    v, pts = 1.0, []
    for i in range(80):
        v = max(0.98, min(1.32, v + rng.uniform(-0.018, 0.022)))
        x = left + i * (right - left) / 79
        y = bottom - (v - 1.0) / 0.32 * (bottom - top)
        pts.append((x, y))
    for i in range(1, len(pts)):
        c.line(pts[i - 1][0], pts[i - 1][1], pts[i][0], pts[i][1], (232, 82, 77), 2)
    for i in range(0, left + 634, 12):
        c.line(i, top + 60, min(i + 6, right), top + 60, (178, 187, 198))
    c.text(20, 14, "2026-09 = 1.00", fg, 2)
    c.text(560, 14, "+18.6%", (232, 82, 77), 2)
    c.text(20, bottom + 20, "09-01", fg, 1)
    c.text(320, bottom + 20, "10-15", fg, 1)
    c.text(620, bottom + 20, "11-09", fg, 1)
    write_png(path, c)


def statement_chart(path: Path):
    """交割单表格截图（纯数字排版，不含真实标的信息）。"""
    rng = random.Random(2026)
    w, h = 720, 400
    c = Canvas(w, h, (255, 255, 255))
    head, line, fg = (32, 44, 58), (227, 232, 238), (78, 88, 100)
    c.rect(20, 20, 700, 62, head, True)
    c.text(36, 34, "2026-11-03", (240, 244, 248), 2)
    c.text(560, 34, "# 12024", (240, 244, 248), 2)
    cols = (36, 180, 310, 430, 560)
    for i in range(9):
        y = 92 + i * 30
        c.line(20, y + 26, 700, y + 26, line)
        vals = (
            "2026-11-0%d" % (i % 9 + 1),
            "%d.%02d" % (100 + i, rng.randint(10, 99)),
            "%d.%02d" % (101 + i, rng.randint(10, 99)),
            "%d" % rng.randint(1, 9),
            ("+", "-")[i % 3 == 0] + "%d.%02d" % (rng.randint(0, 4), rng.randint(10, 99)),
        )
        for x, val in zip(cols, vals):
            c.text(x, y, val, fg, 1)
    c.rect(20, 360, 700, 362, head, True)
    c.text(36, 370, "06", fg, 1)
    c.text(560, 370, "+3.42%", (232, 82, 77), 2)
    write_png(path, c)


def peer_image(path: Path):
    """群友发的图：只在 mock/media，不应被投影拷进 dist/media。"""
    c = Canvas(360, 180, (246, 248, 251))
    c.rect(0, 0, 359, 179, (206, 214, 224))
    c.text(120, 78, "2026-11-09", (110, 120, 132), 2)
    write_png(path, c)


def write_csv(path: Path):
    rows = [
        "date,symbol,side,qty,price,pnl",
        "2026-11-02,DEMO-A,BUY,2,101.20,0.00",
        "2026-11-03,DEMO-A,SELL,2,103.45,+4.50",
        "2026-11-04,DEMO-B,BUY,1,98.70,0.00",
        "2026-11-05,DEMO-B,SELL,1,97.90,-0.80",
        "2026-11-06,DEMO-C,BUY,3,55.10,0.00",
        "2026-11-09,DEMO-C,SELL,3,56.80,+5.10",
    ]
    path.write_text("\n".join(rows) + "\n", encoding="utf-8")


def write_pdf(path: Path, title: str, lines):
    """最小可用 PDF（1 页 / Helvetica），仅用于演示附件链接可打开。"""
    def esc(s):
        return s.replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")

    content = "BT /F1 17 Tf 48 770 Td (" + esc(title) + ") Tj ET\n"
    for i, line in enumerate(lines):
        content += "BT /F1 11 Tf 48 %d Td (%s) Tj ET\n" % (735 - i * 22, esc(line))
    objs = [
        "<< /Type /Catalog /Pages 2 0 R >>",
        "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] "
        "/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
        "<< /Length %d >>\nstream\n%s\nendstream" % (len(content.encode("latin-1")), content),
        "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    ]
    out = bytearray(b"%PDF-1.4\n")
    offsets = []
    for i, obj in enumerate(objs, 1):
        offsets.append(len(out))
        out += ("%d 0 obj\n%s\nendobj\n" % (i, obj)).encode("latin-1")
    xref = len(out)
    out += ("xref\n0 %d\n" % (len(objs) + 1)).encode()
    out += b"0000000000 65535 f \n"
    for off in offsets:
        out += ("%010d 00000 n \n" % off).encode()
    out += ("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n"
            % (len(objs) + 1, xref)).encode()
    path.write_bytes(bytes(out))


def to_jpeg(png: Path, jpg: Path) -> bool:
    """macOS 自带 sips；不可用时回退 PNG。"""
    try:
        r = subprocess.run(["sips", "-s", "format", "jpeg", str(png), "--out", str(jpg)],
                           capture_output=True, timeout=30)
        return r.returncode == 0 and jpg.exists() and jpg.stat().st_size > 0
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return False


# --------------------------------------------------------------------------
# 生成 mock 事实源
# --------------------------------------------------------------------------
def build_source():
    media = MOCK / "media"
    raw = MOCK / "data" / "raw"
    if MOCK.exists():
        shutil.rmtree(MOCK)
    media.mkdir(parents=True)
    raw.mkdir(parents=True)

    candle_chart(media / "12011.png", True, 11, "2026-10-09")
    statement_chart(media / "12024.png")
    equity_chart(media / "12014.png")
    equity_chart(media / "12027.png")
    peer_image(media / "12037.png")
    write_csv(media / "12013.csv")
    write_pdf(media / "12030.pdf", "Weekly Notes (MOCK DATA)", [
        "This is a generated demo attachment. No real trading data.",
        "Week 45: 6 planned actions, 1 unplanned action.",
        "Rule for next week: write down the reason before entry.",
        "Generated by scripts/mock_data.py",
    ])

    jpeg_ok = True
    for mid in JPEG_IDS:
        png, jpg = media / f"{mid}.png", media / f"{mid}.jpg"
        if not to_jpeg(png, jpg):
            jpeg_ok = False
            break
    if not jpeg_ok:
        for jpg in media.glob("*.jpg"):
            jpg.unlink()
    else:
        for mid in JPEG_IDS:
            (media / f"{mid}.png").unlink(missing_ok=True)

    rows = messages(jpeg_ok)
    groups = {}
    for r in rows:
        groups.setdefault(r["month"], []).append(r)
    for month, items in groups.items():
        items.sort(key=lambda x: x["id"])
        (raw / f"{month}.json").write_text(
            json.dumps(items, ensure_ascii=False, indent=2), encoding="utf-8")

    state = {
        "last_msg_id": max(r["id"] for r in rows),
        "target_user_id": TRADER,
        "total": len(rows),
        "updated_at": datetime.now(timezone.utc).isoformat(),
        "_mock": True,
    }
    (MOCK / "data" / "state.json").write_text(
        json.dumps(state, ensure_ascii=False, indent=2), encoding="utf-8")
    return rows, state, jpeg_ok


def build_preview(with_context: bool):
    """在临时工区跑官方 build_view.py，再把产物拷到 dist/ 并内联数据。"""
    tmp = Path(tempfile.mkdtemp(prefix="tg-mock-build-"))
    try:
        (tmp / "data" / "raw").mkdir(parents=True)
        for f in (MOCK / "data" / "raw").glob("*.json"):
            shutil.copy2(f, tmp / "data" / "raw" / f.name)
        shutil.copy2(MOCK / "data" / "state.json", tmp / "data" / "state.json")
        shutil.copytree(MOCK / "media", tmp / "media")
        for name in ("index.html", "i18n.json"):
            shutil.copy2(ROOT / name, tmp / name)

        env = dict(os.environ)
        env.update({
            "DISPLAY_NAME": "阿哲 · 交易笔记",
            "DISPLAY_NAME_ZH_CN": "阿哲 · 交易笔记",
            "DISPLAY_NAME_ZH_TW": "阿哲 · 交易筆記",
            "DISPLAY_NAME_EN": "Zhe's Trading Notes",
            "INCLUDE_CONTEXT": "1" if with_context else "0",
            "ANONYMIZE_CONTEXT": "1",
        })
        subprocess.run([sys.executable, str(ROOT / "scripts" / "build_view.py")],
                       cwd=tmp, env=env, check=True)

        if DIST.exists():
            shutil.rmtree(DIST)
        shutil.copytree(tmp / "dist", DIST)

        def inline(obj):
            return (json.dumps(obj, ensure_ascii=False)
                    .replace("<", "\\u003c")
                    .replace("\u2028", "\\u2028")
                    .replace("\u2029", "\\u2029"))

        i18n = json.loads((DIST / "i18n.json").read_text(encoding="utf-8"))
        posts = json.loads((DIST / "data" / "posts.json").read_text(encoding="utf-8"))
        inject = ("<script>window.__ARCHIVE_MOCK__=true;"
                  "window.__ARCHIVE_I18N__=" + inline(i18n) + ";"
                  "window.__ARCHIVE_POSTS__=" + inline(posts) + ";</script>\n")
        html = DIST / "index.html"
        s = html.read_text(encoding="utf-8")
        if "</head>" not in s:
            raise SystemExit("index.html 缺少 </head>，无法注入预览数据")
        html.write_text(s.replace("</head>", inject + "</head>", 1), encoding="utf-8")
        return posts
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    ap = argparse.ArgumentParser(description="生成 mock 事实源与离线可交互预览")
    ap.add_argument("--no-context", action="store_true", help="不生成回复上下文")
    args = ap.parse_args()

    rows, state, jpeg_ok = build_source()
    posts = build_preview(not args.no_context)

    months = sorted({r["month"] for r in rows})
    print("mock 事实源：mock/data/raw/ + mock/data/state.json")
    print("  全群消息 %d 条，发送者 %d 个，月份 %s" %
          (len(rows), len({r["sender_id"] for r in rows}), ", ".join(months)))
    print("  已删除 %d 条，带媒体 %d 条，带回复 %d 条" %
          (sum(r["deleted"] for r in rows), sum(bool(r["media"]) for r in rows),
           sum(bool(r["reply_to_msg_id"]) for r in rows)))
    print("  JPEG 转换：%s" % ("成功（含 .jpg 分支）" if jpeg_ok else "不可用，已回退 .png"))
    print("预览产物：dist/ （已内联 i18n + posts，可直接 file:// 打开）")
    print("  展示条目 %d 条，其中带上下文 %d 条，站点媒体 %d 个" %
          (posts["count"],
           sum("reply_context" in p for p in posts["posts"]),
           len(list((DIST / "media").glob("*")))))
    print("  注意：mock/ 与 dist/ 均不写入 data/，不影响真实游标。")


if __name__ == "__main__":
    main()
