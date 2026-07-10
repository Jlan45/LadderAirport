# 快速部署 Agent（systemd）

## 一键安装（推荐：从 Release 拉最新二进制）

节点上**无需 Go / 源码 / 子模块**，只要能访问 GitHub：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TOKEN='你的长密钥' bash
```

**默认开启 TLS**（节点本地自签 CA + 服务端证书）。未设置 `LADDER_TOKEN` 时脚本会随机生成并打印，**请保存到 Panel**。

明文 lab（不推荐公网）：

```bash
curl -fsSL ... | sudo LADDER_TLS=0 LADDER_TOKEN='secret' bash
```

指定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TOKEN='secret' LADDER_VERSION=v0.2.0 bash
```

### 行为说明

| 步骤 | 内容 |
|------|------|
| 下载 | `ladder-agent-linux-amd64` 或 `arm64`（按 `uname -m`） |
| 校验 | 若 Release 含 `SHA256SUMS.txt` 则自动校验 |
| 安装 | `/usr/local/bin/ladder-agent` |
| TLS | 默认 `LADDER_TLS=1`：生成 `/etc/ladder-agent/tls/{ca,server}.*` |
| 配置 | `/etc/ladder-agent/agent.env`（已存在则不覆盖 Token） |
| 登记提示 | `/etc/ladder-agent/panel-import.txt`（含 Token + CA PEM） |
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
| **ca_cert_pem** | 粘贴 `/etc/ladder-agent/tls/ca.crt` 全文（或 `panel-import.txt` 中 CA 段） |
| tls_skip_verify | **false**（已贴 CA 时不要开） |

```bash
# 快速查看登记信息
sudo cat /etc/ladder-agent/panel-import.txt
sudo cat /etc/ladder-agent/tls/ca.crt
```

Panel 开启 bootstrap 时会自动下发配置并启动 sing-box。

### TLS 说明

- **无密钥协商**：证书在安装时生成；Panel 用 CA 校验 Agent 服务端证书。
- SAN 自动包含：localhost、本机 hostname、`hostname -I`、尽力探测的公网 IP；可用 `LADDER_TLS_EXTRA_SANS` 追加。
- 证书已存在则复用；强制重签：`sudo rm -rf /etc/ladder-agent/tls` 后重新执行安装（`LADDER_TLS=1`），并**更新 Panel 上的 ca_cert_pem**。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `LADDER_TOKEN` | 随机生成 | 与 Panel 节点 Token 一致 |
| `LADDER_LISTEN` | `0.0.0.0:50051` | gRPC 监听 |
| `LADDER_TLS` | `1` | `1` 生成并启用 TLS；`0` 明文 |
| `LADDER_TLS_DAYS` | `825` | 证书有效期（天） |
| `LADDER_TLS_CN` | hostname | 证书 CN |
| `LADDER_TLS_EXTRA_SANS` | 空 | 额外 SAN，逗号分隔，如 `DNS:node1.example.com,IP:203.0.113.10` |
| `LADDER_VERSION` | `latest` | Release 标签，如 `v0.2.0` |
| `LADDER_FROM` | `release` | `release` 下载；`local` 源码/本地 bin |
| `LADDER_REPO` | `Jlan45/LadderAirport` | GitHub 仓库 |
| `INSTALL_BIN` | `/usr/local/bin/ladder-agent` | 安装路径 |

## 常用命令

```bash
systemctl status ladder-agent
journalctl -u ladder-agent -f
systemctl restart ladder-agent

# 升级到最新 Release（保留 agent.env 与 TLS 证书）
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
