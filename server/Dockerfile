# 构建上下文 = server/ 目录（不是仓库根目录）
#   cd server && docker build -t tg-service:latest .
FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/terry-alerts ./cmd/server

# 运行阶段：alpine 自带 /bin/sh + busybox nc，可做容器健康检查
FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/terry-alerts /app/terry-alerts
ENTRYPOINT ["/app/terry-alerts"]
