# 快速部署 Agent（systemd）

## 一键安装（推荐：从 Release 拉最新二进制）

节点上**无需 Go / 源码 / 子模块**，只要能访问 GitHub：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TOKEN='你的长密钥' bash
```

未设置 `LADDER_TOKEN` 时脚本会随机生成并打印，**请保存到 Panel**。

指定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TOKEN='secret' LADDER_VERSION=v0.1.0 bash
```

### 行为说明

| 步骤 | 内容 |
|------|------|
| 下载 | `ladder-agent-linux-amd64` 或 `arm64`（按 `uname -m`） |
| 校验 | 若 Release 含 `SHA256SUMS.txt` 则自动校验 |
| 安装 | `/usr/local/bin/ladder-agent` |
| 配置 | `/etc/ladder-agent/agent.env`（已存在则不覆盖） |
| 服务 | `ladder-agent.service` enable + restart |

### 其他安装方式

```bash
# 使用本地已有二进制
sudo LADDER_TOKEN='secret' ./scripts/install-agent.sh /path/to/ladder-agent

# 在源码仓库内本地编译再装
cd LadderAirport && git submodule update --init --recursive
sudo LADDER_TOKEN='secret' LADDER_FROM=local ./scripts/install-agent.sh
```

## 装完后在 Panel 登记

| 字段 | 值 |
|------|-----|
| Address | 节点 IP（对 Panel 可达，跨机勿用 `127.0.0.1`） |
| gRPC 端口 | `50051`（默认） |
| Token | 与 `/etc/ladder-agent/agent.env` 中 `LADDER_TOKEN` 一致 |

Panel 开启 bootstrap 时会自动下发配置并启动 sing-box。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `LADDER_TOKEN` | 随机生成 | 与 Panel 节点 Token 一致 |
| `LADDER_LISTEN` | `0.0.0.0:50051` | gRPC 监听 |
| `LADDER_VERSION` | `latest` | Release 标签，如 `v0.1.0` |
| `LADDER_FROM` | `release` | `release` 下载；`local` 源码/本地 bin |
| `LADDER_REPO` | `Jlan45/LadderAirport` | GitHub 仓库 |
| `INSTALL_BIN` | `/usr/local/bin/ladder-agent` | 安装路径 |

## 常用命令

```bash
systemctl status ladder-agent
journalctl -u ladder-agent -f
systemctl restart ladder-agent

# 升级到最新 Release（保留 agent.env）
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh | sudo bash
```

## 防火墙

```bash
# 仅放行 Panel 网段访问 gRPC（示例）
sudo ufw allow from 10.0.0.0/8 to any port 50051 proto tcp
```

入站代理端口（SS/VLESS 等）按业务再放行。

## 监听特权端口（&lt;1024）

默认 unit 使用普通用户 `ladder`。若入站需要 443 等端口：

- 改 unit 为 `User=root`（不推荐），或  
- `setcap 'cap_net_bind_service=+ep' /usr/local/bin/ladder-agent` 并调整加固选项。
