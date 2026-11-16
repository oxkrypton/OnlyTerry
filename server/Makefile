BINARY := terry-alerts
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test build linux-amd64 linux-arm64 release clean

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
