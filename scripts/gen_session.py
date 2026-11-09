#!/usr/bin/env python3
"""仅在本地跑一次：生成 StringSession 供 CI 使用。

    pip install telethon
    python scripts/gen_session.py

输出的超长字符串存入 GitHub Secrets 的 TG_SESSION。
该字符串等于账号完全控制权，请用专用小号，不要外泄。
"""
from telethon.sync import TelegramClient
from telethon.sessions import StringSession

api_id = int(input("API_ID: "))
api_hash = input("API_HASH: ").strip()

with TelegramClient(StringSession(), api_id, api_hash) as client:
    print("\n--- 复制下方字符串存入 Secrets: TG_SESSION ---")
    print(client.session.save())
    print("---------------------------------------------")
