# LadderAirport

自建 **代理机群控制面**：Go 实现的 Panel（内嵌 React 前端 + SQLite）+ 基于 sing-box 二开的节点 Agent，Panel 通过 **gRPC**（可选 TLS + 共享 Token）批量管控节点。

仓库：https://github.com/Jlan45/LadderAirport

## 功能概览

| 能力 | 说明 |
|------|------|
| 节点管理 | 登记 Agent、探测连通、启停核心、卡片/表格总览 |
| 入站模板 | Shadowsocks / Trojan / VLESS(Reality) / Hysteria2，密钥自动生成 |
| 配置下发 | 入站关联到节点 → 转成完整 sing-box JSON → gRPC 热更新 |
| 启动同步 | Panel 启动自动下发并 Start；未上线节点定时重试 |
| 订阅 | 生成 **Clash** / **sing-box** 订阅链接，基础 **CN 分流**（大陆/局域网直连） |
| 部署 | 一键安装 Agent 为 systemd 服务 |

## 架构

```
浏览器 --HTTP--> Panel（Go + 内嵌 SPA + SQLite）
                    │
                    │ gRPC（可选 TLS + Bearer Token）
                    ├──► Agent-1（ladder-agent + 进程内 sing-box）
                    ├──► Agent-2
                    └──► Agent-N
```

| 目录 | 作用 |
|------|------|
| `panel/` | 控制面：HTTP API、鉴权、存储、转换器、批量任务、节点 gRPC 客户端 |
| `agent/` | 节点端 `ladder-agent`：AgentControl gRPC + Box 生命周期 |
| `agent/sing-box/` | 上游 sing-box 子模块（钉版本） |
| `web/` | React + TypeScript + Vite 源码（构建进 `panel/web/dist` 供 embed） |
| `pkg/` | 共享库（Token 鉴权等） |
| `proto/` | gRPC 定义与生成代码 |
| `scripts/` | 安装、e2e、证书等脚本 |
| `deploy/` | systemd 单元与环境变量示例 |

**职责边界：** Panel 负责协议模板、表单参数、转 JSON、批量下发；Agent 只接收完整配置并跑核心，不解析业务模板。

设计文档：

- [设计说明](docs/superpowers/specs/2026-07-09-ladder-airport-design.md)
- [实现计划](docs/superpowers/plans/2026-07-09-ladder-airport.md)

## 依赖环境

- **Go** 1.22+（模块要求见各 `go.mod`，CI 使用 `GOTOOLCHAIN=auto`）
- **Node.js** 20+（改前端时需要；仓库已提交 `panel/web/dist`，纯后端构建可不装）
- **Git 子模块**（构建 Agent 必须）

```bash
git clone --recurse-submodules https://github.com/Jlan45/LadderAirport.git
cd LadderAirport
# 若已克隆未带子模块：
git submodule update --init --recursive
```

## 编译

```bash
make agent   # → bin/ladder-agent
make panel   # 会先 build web，→ bin/panel
make web     # 仅前端，写入 panel/web/dist
make test    # pkg / panel / agent 单测
make proto   # 重新生成 gRPC 代码（需 protoc）
```

仅用已提交的前端产物编 Panel（无需 npm）：

```bash
cd panel && go build -o ../bin/panel ./cmd/panel
```

## 本地运行

### 1. 启动 Agent（节点）

```bash
./bin/ladder-agent \
  -listen 0.0.0.0:50051 \
  -token test \
  -data-dir /tmp/ladder-agent
```

可选 TLS：

```bash
./scripts/gen-dev-certs.sh
./bin/ladder-agent -listen 0.0.0.0:50051 -token test -data-dir /tmp/ladder-agent \
  -tls-cert deploy/dev/agent.crt -tls-key deploy/dev/agent.key
```

### 2. 启动 Panel

```bash
./bin/panel -listen :8080 -db ./data/panel.db -session-secret '请换成长随机串'
```

浏览器打开：http://127.0.0.1:8080  

首次管理员密码默认 **`admin`**（请立刻在「设置」中修改）。

### 3. 登记节点

| 字段 | 填写 |
|------|------|
| Address | 节点 IP（对 Panel 可达；跨机勿填 `127.0.0.1`） |
| gRPC 端口 | `50051` |
| Token | 与 Agent 的 `-token` 一致 |

创建入站 → 节点详情里关联 → 下发配置。  
Panel 默认会在启动时 **自动下发并启动** 所有节点，并对未上线节点定时重试。

### 常用 Panel 参数

| 参数 | 默认 | 说明 |
|------|------|------|
| `-listen` | `:8080` | HTTP 监听 |
| `-db` | `./data/panel.db` | SQLite 路径 |
| `-session-secret` | 随机临时 | 建议固定，避免重启掉登录态 |
| `-bootstrap` | `true` | 启动时全量下发 + Start |
| `-bootstrap-timeout` | `3m` | 首次 bootstrap 超时 |
| `-bootstrap-retry` | `true` | 定时重试未就绪节点 |
| `-bootstrap-retry-interval` | `30s` | 重试间隔 |

### 一键装 Agent 为系统服务

**无需克隆仓库**，从 GitHub Release 拉最新二进制（详见 [deploy/README-agent.md](deploy/README-agent.md)）：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TOKEN='你的密钥' bash
```

指定版本 / 本地编译：

```bash
# 指定 Release 标签
curl -fsSL ... | sudo LADDER_VERSION=v0.1.0 LADDER_TOKEN='secret' bash

# 仓库内本地编译安装
sudo LADDER_FROM=local LADDER_TOKEN='secret' ./scripts/install-agent.sh
```

```bash
systemctl status ladder-agent
journalctl -u ladder-agent -f
```

### 烟雾测试

```bash
./scripts/e2e-smoke.sh
```

## 订阅（Clash / sing-box）

1. 保证节点 `Address` 是客户端能连的地址  
2. 入站已关联到节点并已下发  
3. 面板「订阅」创建 **Clash** 或 **sing-box** 链接  
4. 公开拉取：`http://<panel>/sub/<token>`（无需登录）  

分流（基础）：局域网与 **CN** 直连，其余走代理组。  
可在「设置」填写 **Public Base URL** 生成完整订阅 URL。

## CI / 自动构建

推送到 `main` 或打开 PR 时，GitHub Actions 会：

1. 拉取子模块  
2. 跑 `pkg` / `panel` / `agent` 测试  
3. 编译 `panel` 与 `ladder-agent`（linux/amd64）  
4. 上传构建产物为 Artifacts  

打 `v*` 标签时额外打包 Release 附件。

工作流文件：`.github/workflows/ci.yml`、`.github/workflows/release.yml`。

## 安全建议

- 修改默认管理员密码 `admin`、节点 Token、`-session-secret`
- gRPC Token 错误必须无法管控节点
- 生产环境使用正规 TLS，避免 `tls_skip_verify`
- 浏览器不直连 Agent；仅 Panel 访问 Agent 控制口
- `deploy/dev` 下证书仅限实验室

## 模块路径

Go workspace（`go.work`）：

- `github.com/ladderairport/pkg`
- `github.com/ladderairport/panel`
- `github.com/ladderairport/agent`
- `github.com/ladderairport/proto`

## 许可证 / 上游

Agent 运行时基于 [sing-box](https://github.com/SagerNet/sing-box)（见 `agent/sing-box` 上游协议）。LadderAirport 控制面代码为 monorepo 中独立部分。
