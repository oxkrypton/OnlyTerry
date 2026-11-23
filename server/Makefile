BINARY := terry-alerts
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

-include .env

REMOTE_SSH ?= my-vps
REMOTE_DEPLOY ?= /opt/my-app
REMOTE_DIR ?= $(REMOTE_DEPLOY)/data/service
SERVICE ?= my-service
IMAGE ?= my-service:latest
IMAGE_TAR ?= /tmp/my-service-image.tar.gz

# 镜像部署参数：本机可能是 arm64（Apple Silicon），远端通常是 amd64，默认按远端架构构建。
# IMAGE 约定为 name:tag 形式；DEPLOY_TAG 给出可回滚的版本号，远端会保留上一版为 name:previous。
IMAGE_REPO := $(firstword $(subst :, ,$(IMAGE)))
TARGET_PLATFORM ?= linux/amd64
DEPLOY_TAG := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)-$(shell date +%Y%m%d-%H%M%S)
HEALTHCHECK_URL ?= http://127.0.0.1:3000/healthz

# 这些变量可能只在 .env 里定义，需显式导出给 db-sync.sh
export REMOTE_STOP REMOTE_START PULL_MODE

.DEFAULT_GOAL := test

.PHONY: help test build linux-amd64 linux-arm64 release clean db-pull db-push deploy-image deploy-env deploy-recreate

help:
	@echo "make test             运行 Go 测试（默认）"
	@echo "make build            本地构建二进制"
	@echo "make release          交叉编译并打包 linux-amd64 / linux-arm64"
	@echo "make clean            清理 bin / release"
	@echo "make db-pull          在线拉取数据库（不停服，默认）"
	@echo "make db-pull PULL_MODE=stop  停服后拉取（旧方式）"
	@echo "make db-push          停服上传并备份远端原库"
	@echo "make deploy-image     构建远端架构镜像→推送→重建→健康检查（改代码后）"
	@echo "make deploy-env       scp 本地 .env → 备份远端旧 .env → 重建容器（改配置后）"
	@echo "make deploy-recreate  只重建容器（远端 .env 已手工改好时）"
	@echo "远程参数：REMOTE_SSH / REMOTE_DEPLOY / REMOTE_DIR / SERVICE / IMAGE"
	@echo "构建参数：TARGET_PLATFORM / DEPLOY_TAG / HEALTHCHECK_URL；SKIP_TEST=1 可跳过测试"
	@echo "本机真实值写在 .env（已 gitignore），可复制到另一台机器"

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/server

linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/server

linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 ./cmd/server

release: linux-amd64 linux-arm64
	mkdir -p release
	cd bin && tar -czf ../release/$(BINARY)-linux-amd64.tar.gz $(BINARY)-linux-amd64
	cd bin && tar -czf ../release/$(BINARY)-linux-arm64.tar.gz $(BINARY)-linux-arm64

clean:
	rm -rf bin release

# 远程 SQLite 快照同步：只动 service.db / -wal / -shm，不改 images、session 等文件
# systemd 部署可在 .env 里覆盖 REMOTE_DEPLOY / REMOTE_STOP / REMOTE_START
db-pull:
	REMOTE_SSH="$(REMOTE_SSH)" REMOTE_DEPLOY="$(REMOTE_DEPLOY)" REMOTE_DIR="$(REMOTE_DIR)" SERVICE="$(SERVICE)" bash scripts/db-sync.sh pull

db-push:
	REMOTE_SSH="$(REMOTE_SSH)" REMOTE_DEPLOY="$(REMOTE_DEPLOY)" REMOTE_DIR="$(REMOTE_DIR)" SERVICE="$(SERVICE)" bash scripts/db-sync.sh push

# 镜像部署：测试 → 按远端架构构建（buildx --load）→ 打版本 tag → scp → docker load → force-recreate → 健康检查
# 加载前把远端当前镜像标为 name:previous，健康检查失败时打印回滚命令。
# 数据目录是 bind mount，重建容器不会丢 SQLite / 图片 / session；SKIP_TEST=1 可跳过测试。
deploy-image:
	@if [ "$(SKIP_TEST)" != "1" ]; then echo "→ 运行测试…"; go test ./...; fi
	@echo "→ 构建 $(TARGET_PLATFORM) 镜像：$(IMAGE) + $(IMAGE_REPO):$(DEPLOY_TAG) …"
	docker buildx build --platform $(TARGET_PLATFORM) --build-arg VERSION=$(VERSION) -t $(IMAGE) -t $(IMAGE_REPO):$(DEPLOY_TAG) --load .
	@echo "→ 打包镜像…"
	docker save -o "$(IMAGE_TAR).raw" $(IMAGE) $(IMAGE_REPO):$(DEPLOY_TAG)
	gzip -c "$(IMAGE_TAR).raw" > "$(IMAGE_TAR)" && rm -f "$(IMAGE_TAR).raw"
	@echo "→ 上传镜像到远端…"
	scp "$(IMAGE_TAR)" "$(REMOTE_SSH):/tmp/"
	@echo "→ 保留旧镜像为 $(IMAGE_REPO):previous 并加载新镜像…"
	ssh $(REMOTE_SSH) "docker image inspect $(IMAGE) >/dev/null 2>&1 && docker tag $(IMAGE) $(IMAGE_REPO):previous || true; if docker load < /tmp/$(notdir $(IMAGE_TAR)); then rm -f /tmp/$(notdir $(IMAGE_TAR)); else rm -f /tmp/$(notdir $(IMAGE_TAR)); exit 1; fi"
	@echo "→ 重建容器…"
	ssh $(REMOTE_SSH) "cd '$(REMOTE_DEPLOY)' && docker compose up -d --force-recreate $(SERVICE)"
	@echo "→ 健康检查 $(HEALTHCHECK_URL) …"
	ssh $(REMOTE_SSH) "for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do if curl -fsS $(HEALTHCHECK_URL) >/dev/null 2>&1; then echo '✓ 健康检查通过'; exit 0; fi; sleep 2; done; echo '✗ 健康检查失败' >&2; exit 1" || { echo "可回滚（若 previous 存在）：ssh $(REMOTE_SSH) \"docker tag $(IMAGE_REPO):previous $(IMAGE) && cd '$(REMOTE_DEPLOY)' && docker compose up -d --force-recreate $(SERVICE)\"" >&2; exit 1; }
	rm -f "$(IMAGE_TAR)"

# 上传本地 .env 并重建容器：先备份远端旧 .env，再传文件，最后 force-recreate 注入新环境变量
# restart 不会重新读取 env_file，必须 force-recreate；容器数据走 bind mount，不会丢
deploy-env:
	@test -f .env || { echo "缺少本地 .env，请先在 server/ 下准备好配置文件" >&2; exit 1; }
	ssh $(REMOTE_SSH) "cd '$(REMOTE_DEPLOY)' && if [ -f .env ]; then cp -p .env .env.bak.\$$(date +%Y%m%d-%H%M%S); fi"
	scp .env "$(REMOTE_SSH):$(REMOTE_DEPLOY)/.env"
	ssh $(REMOTE_SSH) "chmod 600 '$(REMOTE_DEPLOY)/.env' && cd '$(REMOTE_DEPLOY)' && docker compose up -d --force-recreate $(SERVICE)"

# 仅重建容器（远端 .env 已手工改好时）：restart 不会重新注入环境变量，必须 force-recreate
deploy-recreate:
	ssh $(REMOTE_SSH) "cd $(REMOTE_DEPLOY) && docker compose up -d --force-recreate $(SERVICE)"
