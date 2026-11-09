PY := $(if $(wildcard .venv/bin/python),.venv/bin/python,python3)
ARGS ?=
.DEFAULT_GOAL := help

.PHONY: help server fetch mock build session

help:
	@echo "make server   本地预览"
	@echo "make fetch    抓取真实数据 (需 .env)"
	@echo "make mock     生成 mock 数据"
	@echo "make build    重建 dist"
	@echo "make session  生成 TG_SESSION"
	@echo "传参: make server ARGS='--port 8080 --open'"

server:
	$(PY) scripts/serve.py $(ARGS)

fetch:
	$(PY) scripts/fetch_tg.py $(ARGS)

mock:
	$(PY) scripts/mock_data.py $(ARGS)

build:
	$(PY) scripts/build_view.py $(ARGS)

session:
	$(PY) scripts/gen_session.py
