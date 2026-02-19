FROM golang:1.26-alpine AS builder

WORKDIR /app

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 编译
RUN CGO_ENABLED=0 GOOS=linux go build -o jmcomic-auto github.com/INKCR0W/jm-automation/cmd/jmcomic-auto

# 运行阶段
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/jmcomic-auto .

# 创建日志目录
RUN mkdir -p logs

# 运行
CMD ["./jmcomic-auto"]
