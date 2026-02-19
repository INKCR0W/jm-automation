# JM Comic Auto

禁漫天堂自动签到工具，支持多账号管理和定时任务。

## 功能

- 自动登录和每日签到
- 多账号支持
- 定时任务调度
- Docker 部署

## 快速开始

### 本地运行

1. 克隆项目

```bash
git clone https://github.com/INKCR0W/jm-automation.git
cd jm-automation
```

2. 配置文件

复制配置模板并修改：

```bash
cp config.example.yaml config.yaml
```

编辑 `config.yaml`，填入你的账号信息：

```yaml
accounts:
  - username: "your_username"
    password: "your_password"
    enabled: true
```

3. 运行

```bash
# 编译
go build -o jmcomic-auto cmd/jmcomic-auto/main.go

# 立即执行一次
./jmcomic-auto -once

# 启动定时任务
./jmcomic-auto
```

### Docker 部署

1. 构建镜像

```bash
docker build -t jmcomic-auto .
```

2. 运行容器

```bash
docker run -d \
  --name jmcomic-auto \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/logs:/app/logs \
  jmcomic-auto
```

### Docker Compose

创建 `docker-compose.yml`：

```yaml
version: '3'
services:
  jmcomic-auto:
    build: .
    container_name: jmcomic-auto
    volumes:
      - ./config.yaml:/app/config.yaml
      - ./logs:/app/logs
    restart: unless-stopped
```

运行：

```bash
docker-compose up -d
```

## 配置说明

```yaml
server:
  base_url: "https://www.cdnsha.org"  # API 地址
  timeout: 30                          # 请求超时（秒）

accounts:
  - username: "user1"
    password: "pass1"
    enabled: true                      # 必须设置为 true
  - username: "user2"
    password: "pass2"
    enabled: false                     # 禁用此账号

scheduler:
  cron: "0 0 9 * * *"                  # 每天 9:00 执行
  timezone: "Asia/Shanghai"            # 时区

log:
  level: "info"                        # 日志级别
  file: "logs/app.log"
  max_size: 100                        # 单个日志文件大小（MB）
  max_backups: 7                       # 保留日志文件数量
```

### Cron 表达式

格式：`秒 分 时 日 月 周`

常用示例：
- `0 0 9 * * *` - 每天 9:00
- `0 30 8 * * *` - 每天 8:30
- `0 0 */6 * * *` - 每 6 小时
- `0 0 0 * * 1` - 每周一 0:00

## 命令行参数

```bash
./jmcomic-auto -h

  -config string
        配置文件路径 (default "config.yaml")
  -once
        立即执行一次任务
  -version
        显示版本信息
```

## 项目结构

```
.
├── cmd/jmcomic-auto/     # 主程序入口
├── internal/
│   ├── api/              # API 接口
│   ├── client/           # HTTP 客户端
│   ├── config/           # 配置管理
│   ├── scheduler/        # 任务调度
│   └── task/             # 任务执行
├── pkg/
│   ├── crypto/           # 加密工具
│   ├── logger/           # 日志
│   └── utils/            # 工具函数
└── config.example.yaml   # 配置模板
```

## 开发

```bash
# 安装依赖
go mod download

# 运行
go run cmd/jmcomic-auto/main.go -once

# 编译
go build -o jmcomic-auto cmd/jmcomic-auto/main.go
```

## License

MIT
