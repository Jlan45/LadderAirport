# 快速部署 Agent（systemd）

> 控制面 Panel 的一键安装见 [README-panel.md](README-panel.md)。

## 一键安装（推荐：从 Release 拉最新二进制）

节点上**无需 Go / 源码 / 子模块**，只要能访问 GitHub：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TOKEN='你的长密钥' bash
```

**默认开启 TLS**（节点本地自签 CA + 服务端证书）。未设置 `LADDER_TOKEN` 时脚本会随机生成并打印，**请保存到 Panel**。

明文 lab（不推荐公网）：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TLS=0 LADDER_TOKEN='你的长密钥' bash
```

指定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TOKEN='你的长密钥' LADDER_VERSION=v0.3.1 bash
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

## 装完后 / 自动 enroll

**推荐：** 在 Panel「设置」填写 **Public Base URL**，再在「节点」点 **添加节点并生成安装命令**。  
生成的命令会带 `LADDER_PANEL` / `LADDER_TOKEN` / `LADDER_NODE_ID`，装完后脚本自动：

`POST {Panel}/api/v1/agent/enroll` → 上报地址、gRPC 端口、CA PEM  

Panel 写回节点记录，**无需手贴 CA / 手填 IP**。刷新列表后点「探测」即可。

| 变量 | 说明 |
|------|------|
| `LADDER_PANEL` | Panel 根 URL，如 `https://panel.example.com` |
| `LADDER_NODE_ID` | 预创建的节点 ID |
| `LADDER_REPORT_ADDRESS` | 强制上报地址（可选，默认自动探测） |
| `LADDER_GRPC_PORT` | 上报端口（默认与监听端口一致） |

```bash
# 手动查看本机材料（自动 enroll 失败时备用）
sudo cat /etc/ladder-agent/panel-import.txt
sudo cat /etc/ladder-agent/tls/ca.crt
```

Panel 开启 bootstrap 时会自动下发配置并启动 sing-box。

### TLS 说明

- **无密钥协商**：证书在安装时生成；Panel 用 CA 校验 Agent 服务端证书。
- SAN 自动包含：localhost、本机 hostname、`hostname -I`、尽力探测的公网 IP；可用 `LADDER_TLS_EXTRA_SANS` 追加。
- 证书已存在则复用；强制重签：`sudo rm -rf /etc/ladder-agent/tls` 后重新执行安装（`LADDER_TLS=1`），并**更新 Panel 上的 ca_cert_pem**。

## NAT / 端口转发

控制面仍是 **Panel 主动 dial Agent gRPC**。Agent 在 NAT 后时，需要上层做端口转发（或 VPN），让 Panel 能连到 Agent。

```
[客户端] --入站端口--> [公网 IP / DDNS] --DNAT--> [Agent 入站]
[Panel]  --gRPC------> [VPN 或映射的 gRPC] ------> [Agent :50051]
```

| 配置项 | 填什么 |
|--------|--------|
| 控制面地址 `address` | **Panel 能拨到的** host（公网 IP、DDNS、VPN IP） |
| 控制面端口 `grpc_port` | **外部映射端口**（若 `15051→50051` 则填 `15051`） |
| 公网地址 `public_address` | 订阅客户端用的 host；与控制面相同时可留空（回退 `address`） |

建议流程：

1. Agent 监听 `0.0.0.0:50051`，防火墙放行 Panel 源与客户端入站端口。  
2. 路由器/云安全组做 DNAT：外网 gRPC 端口 → 本机 50051；各入站端口按需映射。  
3. TLS：若 Panel 拨的是公网 IP/域名，安装时加 `LADDER_TLS_EXTRA_SANS=IP:x.x.x.x,DNS:name`。  
4. 在 Panel 节点详情填写控制面地址/映射端口；客户端入口不同再填公网地址。  
5. 首次装机可用 `LADDER_REPORT_ADDRESS` 填空地址；**之后重装 enroll 不会覆盖已有控制面地址/端口**（仍会更新 CA）。  
6. 点「探测」确认 online，再预览订阅确认 `server` 为客户端可达 host。

相关变量：`LADDER_REPORT_ADDRESS`、`LADDER_GRPC_PORT`、`LADDER_TLS_EXTRA_SANS`（见上文表格）。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `LADDER_ACTION` | `install` | `install` / `upgrade` / `uninstall`（也可用脚本首参） |
| `LADDER_PURGE` | `0` | 仅卸载：`1` 时删除 conf/data/TLS/用户 |
| `LADDER_TOKEN` | 随机生成 | 与 Panel 节点 Token 一致（**upgrade 不会改**） |
| `LADDER_LISTEN` | `0.0.0.0:50051` | gRPC 监听 |
| `LADDER_TLS` | `1` | `1` 生成并启用 TLS；`0` 明文（仅 install） |
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
```

### 升级

只替换二进制、刷新 systemd unit 并 restart；**保留** `agent.env`、TLS 证书与 Token。旧二进制备份为 `/usr/local/bin/ladder-agent.bak`。

```bash
# 升到最新
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo env LADDER_ACTION=upgrade bash

# 升到指定版本
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo env LADDER_ACTION=upgrade LADDER_VERSION=v0.3.1 bash
```

回滚：

```bash
sudo mv /usr/local/bin/ladder-agent.bak /usr/local/bin/ladder-agent
sudo systemctl restart ladder-agent
```

### 卸载

默认只停服务、删 unit 与二进制，**保留** `/etc/ladder-agent` 与 `/var/lib/ladder-agent`。

```bash
# 保留 conf/data
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo env LADDER_ACTION=uninstall bash

# 全清（含 TLS、env、数据目录与系统用户）
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo env LADDER_ACTION=uninstall LADDER_PURGE=1 bash
```

卸载后请在 Panel 中删除对应节点记录。
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
