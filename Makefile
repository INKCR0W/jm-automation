.PHONY: help build build-all clean test lint docker-build docker-push run install

# 变量定义
BINARY_NAME=jmcomic-auto
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

# 默认目标
help:
	@echo "可用的 make 命令："
	@echo "  make build        - 构建当前平台的二进制文件"
	@echo "  make build-all    - 构建所有平台的二进制文件"
	@echo "  make test         - 运行测试"
	@echo "  make lint         - 运行代码检查"
	@echo "  make clean        - 清理构建文件"
	@echo "  make docker-build - 构建 Docker 镜像"
	@echo "  make docker-push  - 推送 Docker 镜像"
	@echo "  make run          - 运行程序"
	@echo "  make install      - 安装到系统"

# 构建当前平台
build:
	@echo "构建 $(BINARY_NAME) $(VERSION)..."
	go build $(LDFLAGS) -o $(BINARY_NAME) cmd/jmcomic-auto/main.go
	@echo "构建完成: $(BINARY_NAME)"

# 构建所有平台
build-all:
	@echo "构建所有平台..."
	@mkdir -p dist
	
	# Linux AMD64
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 cmd/jmcomic-auto/main.go
	
	# Linux ARM64
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-arm64 cmd/jmcomic-auto/main.go
	
	# Windows AMD64
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe cmd/jmcomic-auto/main.go
	
	# macOS AMD64
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 cmd/jmcomic-auto/main.go
	
	# macOS ARM64
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 cmd/jmcomic-auto/main.go
	
	@echo "所有平台构建完成，文件位于 dist/ 目录"

# 运行测试
test:
	@echo "运行测试..."
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "测试完成，覆盖率报告: coverage.html"

# 代码检查
lint:
	@echo "运行代码检查..."
	@which golangci-lint > /dev/null || (echo "请先安装 golangci-lint: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run --timeout=5m

# 清理
clean:
	@echo "清理构建文件..."
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_NAME).exe
	rm -rf dist/
	rm -f coverage.out coverage.html
	@echo "清理完成"

# Docker 构建
docker-build:
	@echo "构建 Docker 镜像..."
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(BINARY_NAME):$(VERSION) \
		-t $(BINARY_NAME):latest \
		.
	@echo "Docker 镜像构建完成"

# Docker 推送
docker-push:
	@echo "推送 Docker 镜像..."
	docker push $(BINARY_NAME):$(VERSION)
	docker push $(BINARY_NAME):latest
	@echo "Docker 镜像推送完成"

# 运行程序
run: build
	@echo "运行 $(BINARY_NAME)..."
	./$(BINARY_NAME)

# 安装到系统
install: build
	@echo "安装 $(BINARY_NAME) 到系统..."
	@mkdir -p $(HOME)/.local/bin
	cp $(BINARY_NAME) $(HOME)/.local/bin/
	@echo "安装完成: $(HOME)/.local/bin/$(BINARY_NAME)"
	@echo "请确保 $(HOME)/.local/bin 在您的 PATH 中"

# 开发模式（立即执行一次）
dev: build
	@echo "开发模式：立即执行一次任务..."
	./$(BINARY_NAME) -once

# 显示版本信息
version:
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
