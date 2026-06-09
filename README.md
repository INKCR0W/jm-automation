# JM Comic Auto

[![CI](https://github.com/INKCR0W/jm-automation/actions/workflows/ci.yml/badge.svg)](https://github.com/INKCR0W/jm-automation/actions/workflows/ci.yml)
[![Release](https://github.com/INKCR0W/jm-automation/actions/workflows/release.yml/badge.svg)](https://github.com/INKCR0W/jm-automation/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/INKCR0W/jm-automation)](https://goreportcard.com/report/github.com/INKCR0W/jm-automation)
[![License](https://img.shields.io/github/license/INKCR0W/jm-automation)](LICENSE)
[![GitHub release](https://img.shields.io/github/v/release/INKCR0W/jm-automation)](https://github.com/INKCR0W/jm-automation/releases)

禁漫天堂自动签到工具，支持多账号管理和定时任务。

## 功能

- 自动登录和每日签到
- 多账号支持
- 定时任务调度
- 随机延迟
- Docker 部署

## 致谢

- 签到相关逻辑参考了 [Breeze](https://github.com/deretame/Breeze) 项目的实现思路，特此致谢。

## 快速开始

### 下载

#### 稳定版本（推荐）
访问 [Releases](https://github.com/INKCR0W/jm-automation/releases) 页面下载最新的稳定版本。

#### 开发版本
如果你想体验最新功能，可以下载 [Pre-release](https://github.com/INKCR0W/jm-automation/releases/tag/pre-release) 版本（每次代码更新后自动构建）。

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

### Docker Compose（推荐）

1. 准备配置文件

```bash
cp config.example.yaml config.yaml
# 编辑 config.yaml，填入账号信息
```

2. 启动服务

```bash
docker-compose up -d
```

3. 查看日志

```bash
docker-compose logs -f
```

4. 停止服务

```bash
docker-compose down
```

### Docker 手动部署

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
  random_delay: 30                     # 随机延迟（分钟），0 表示不延迟

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

### 随机延迟

`random_delay` 配置项用于在定时任务触发后添加随机延迟，模拟人工操作：
- 设置为 `30` 表示在触发时间后 0-30 分钟内随机执行
- 设置为 `0` 表示立即执行，不添加延迟
- 例如 cron 设置为 9:00，random_delay 为 30，则实际执行时间在 9:00-9:30 之间

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

GPL-3.0

本项目采用 GNU General Public License v3.0 开源协议。

这意味着：
- 你可以自由使用、修改和分发本项目
- 如果你修改或基于本项目创建衍生作品，必须同样使用 GPL-3.0 协议开源
- 必须保留原作者的版权声明和许可证声明
- 不提供任何担保

详见 [LICENSE](LICENSE) 文件。
