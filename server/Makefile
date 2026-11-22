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

# 这些变量可能只在 .env 里定义，需显式导出给 db-sync.sh
export REMOTE_STOP REMOTE_START PULL_MODE

.DEFAULT_GOAL := test

.PHONY: help test build linux-amd64 linux-arm64 release clean db-pull db-push deploy-image deploy-recreate

help:
	@echo "make test             运行 Go 测试（默认）"
	@echo "make build            本地构建二进制"
	@echo "make release          交叉编译并打包 linux-amd64 / linux-arm64"
	@echo "make clean            清理 bin / release"
	@echo "make db-pull          在线拉取数据库（不停服，默认）"
	@echo "make db-pull PULL_MODE=stop  停服后拉取（旧方式）"
	@echo "make db-push          停服上传并备份远端原库"
	@echo "make deploy-image     本地构建镜像→推送→重建容器（改代码后）"
	@echo "make deploy-recreate  只重建容器（改 .env 后）"
	@echo "远程参数：REMOTE_SSH / REMOTE_DEPLOY / REMOTE_DIR / SERVICE / IMAGE"
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
	REMOTE_SSH="$(REMOTE_SSH)" REMOTE_DEPLOY="$(REMOTE_DEPLOY)" REMOTE_DIR="$(REMOTE_DIR)" bash scripts/db-sync.sh pull

db-push:
	REMOTE_SSH="$(REMOTE_SSH)" REMOTE_DEPLOY="$(REMOTE_DEPLOY)" REMOTE_DIR="$(REMOTE_DIR)" bash scripts/db-sync.sh push

# 镜像部署：构建 → 打包 → scp → docker load → 强制重建容器
# 数据目录是 bind mount，重建容器不会丢 SQLite / 图片 / session。
deploy-image:
	docker build -t $(IMAGE) .
	docker save $(IMAGE) | gzip > $(IMAGE_TAR)
	scp $(IMAGE_TAR) $(REMOTE_SSH):/tmp/
	ssh $(REMOTE_SSH) "docker load < /tmp/$(notdir $(IMAGE_TAR)) && cd $(REMOTE_DEPLOY) && docker compose up -d --force-recreate $(SERVICE)"
	ssh $(REMOTE_SSH) "rm -f /tmp/$(notdir $(IMAGE_TAR))"

# 仅改了远端 .env 时用：restart 不会重新注入环境变量，必须 force-recreate
deploy-recreate:
	ssh $(REMOTE_SSH) "cd $(REMOTE_DEPLOY) && docker compose up -d --force-recreate $(SERVICE)"
