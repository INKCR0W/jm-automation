FROM golang:1.26-alpine AS builder

# 构建参数
ARG VERSION=dev
ARG BUILD_TIME=unknown

WORKDIR /app

# 安装构建依赖
RUN apk add --no-cache git

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 编译（添加版本信息和优化）
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -installsuffix cgo \
    -ldflags="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
    -o jmcomic-auto \
    ./cmd/jmcomic-auto

# 运行阶段
FROM alpine:latest

# 安装运行时依赖
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/jmcomic-auto .

# 创建启动脚本
RUN echo '#!/bin/sh' > /app/entrypoint.sh && \
    echo 'mkdir -p /app/logs /app/data/cookies' >> /app/entrypoint.sh && \
    echo 'exec ./jmcomic-auto "$@"' >> /app/entrypoint.sh && \
    chmod +x /app/entrypoint.sh

# 添加非 root 用户
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser && \
    chown -R appuser:appuser /app

# 切换到非 root 用户
USER appuser

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD pgrep -f jmcomic-auto || exit 1

# 运行
CMD ["/app/entrypoint.sh"]

