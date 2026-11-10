#!/usr/bin/env python3
"""本地开发服务器：用源码前端 + 已构建的数据，复现 GitHub Pages 的相对路径行为。

用法：
    python3 scripts/serve.py                      # http://127.0.0.1:8000/
    python3 scripts/serve.py --port 8080 --open  # 换端口并自动开浏览器
    python3 scripts/serve.py --rebuild           # 启动前重新生成 mock 数据
    python3 scripts/serve.py --dist              # 纯 dist 静态站点（等价线上产物）

数据准备（二选一）：
    python3 scripts/mock_data.py                 # mock 数据（当前状态）
    python3 scripts/build_view.py               # 已抓到 data/session*/raw/ 后，用真实数据重建 dist/
"""
from __future__ import annotations

import argparse
import http.server
import json
import socketserver
import subprocess
import sys
import webbrowser
from pathlib import Path
from urllib.parse import unquote, urlparse

ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
FRONTEND = ROOT / "frontend"
# source 模式下这些路径直接读前端源码，其余（data/ media/）仍走已构建的 dist/
SOURCE_FILES = {
    "": "index.html",
    "index.html": "index.html",
    "i18n.json": "i18n.json",
    "avatar.webp": "avatar.webp",
}


class DevHandler(http.server.SimpleHTTPRequestHandler):
    server_version = "TGArchiveDev/1.0"

    def translate_path(self, path):
        rel = unquote(urlparse(path).path).lstrip("/")
        # 防目录穿越：任何 .. 都落到不存在的路径
        if ".." in Path(rel).parts:
            return str(DIST / "__blocked__")
        if self.server.source_mode and rel in SOURCE_FILES:
            return str(FRONTEND / SOURCE_FILES[rel])
        return str(DIST / rel)

    def end_headers(self):
        # 开发时禁用缓存，改完刷新立刻生效
        self.send_header("Cache-Control", "no-store, must-revalidate")
        super().end_headers()

    def log_message(self, fmt, *args):
        if not self.server.quiet:
            super().log_message(fmt, *args)


class Server(socketserver.TCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main():
    ap = argparse.ArgumentParser(description="本地跑起发言归档站（带数据）")
    ap.add_argument("--host", default="127.0.0.1", help="默认 127.0.0.1（仅本机可访问）")
    ap.add_argument("--port", type=int, default=8000, help="默认 8000")
    ap.add_argument("--rebuild", action="store_true", help="启动前重跑 scripts/mock_data.py")
    ap.add_argument("--dist", action="store_true", help="纯 dist 静态站点（不映射源码）")
    ap.add_argument("--open", action="store_true", help="自动打开系统浏览器")
    ap.add_argument("--quiet", action="store_true", help="不打印访问日志")
    args = ap.parse_args()

    if args.rebuild:
        subprocess.run([sys.executable, str(ROOT / "scripts" / "mock_data.py")],
                       cwd=ROOT, check=True)

    posts_file = DIST / "data" / "posts.json"
    if not posts_file.exists():
        raise SystemExit(
            "缺少 dist/data/posts.json，站点会显示「数据加载失败」。\n"
            "先生成数据：python3 scripts/mock_data.py\n"
            "或在启动时加 --rebuild"
        )

    try:
        srv = Server((args.host, args.port), DevHandler)
    except OSError as e:
        raise SystemExit(f"端口 {args.port} 不可用（{e}）。换一个：--port {args.port + 1}")

    srv.quiet = args.quiet
    srv.source_mode = not args.dist

    posts = json.loads(posts_file.read_text(encoding="utf-8"))
    mock_state = ROOT / "mock" / "data" / "state.json"
    is_mock = mock_state.exists() and \
              json.loads((ROOT / "mock" / "data" / "state.json").read_text(encoding="utf-8")).get("_mock")

    url = f"http://{args.host}:{args.port}/"
    print("=" * 62)
    print(f"  站点已启动  {url}")
    print("=" * 62)
    print(f"  数据      {posts_file.relative_to(ROOT)}  （{posts['count']} 条发言，"
          f"{'MOCK 演示数据' if is_mock else '已构建数据'}）")
    print(f"  页面      {'frontend/index.html（源码，改完刷新即生效）' if srv.source_mode else 'dist/index.html（纯线上产物）'}")
    print(f"  媒体      dist/media/   语言包  {'i18n.json' if srv.source_mode else 'dist/i18n.json'}")
    print("  停止      Ctrl+C")
    print("=" * 62, flush=True)

    if args.open:
        webbrowser.open(url)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        print("\n已停止。")
    finally:
        srv.server_close()


if __name__ == "__main__":
    main()
