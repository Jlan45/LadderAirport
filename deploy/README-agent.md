# 快速部署 Agent（systemd）

## 一键安装（推荐）

在**源码机**先编译，或在目标机有 Go 时直接装：

```bash
# 目标机克隆/拷贝仓库后
cd LadderAirport
git submodule update --init --recursive   # 若尚未拉 sing-box
make agent                                # 或拷贝已编译的 bin/ladder-agent

sudo LADDER_TOKEN='你的长密钥' ./scripts/install-agent.sh
# 未设置 TOKEN 时脚本会随机生成并打印，请保存到 Panel
```

使用已有二进制：

```bash
sudo LADDER_TOKEN='secret' ./scripts/install-agent.sh /path/to/ladder-agent
```

## 装完后在 Panel 登记

| 字段 | 值 |
|------|-----|
| Address | 节点 IP（对 Panel 可达，勿用 127.0.0.1 跨机） |
| gRPC 端口 | `50051`（默认） |
| Token | 与 `/etc/ladder-agent/agent.env` 中 `LADDER_TOKEN` 一致 |

Panel 开启 bootstrap 时会自动下发配置并启动 sing-box。

## 手工 systemd（不跑脚本）

```bash
sudo useradd -r -s /usr/sbin/nologin -d /var/lib/ladder-agent ladder || true
sudo mkdir -p /etc/ladder-agent /var/lib/ladder-agent
sudo cp bin/ladder-agent /usr/local/bin/
sudo cp deploy/agent.env.example /etc/ladder-agent/agent.env
sudo nano /etc/ladder-agent/agent.env   # 改 TOKEN
sudo cp deploy/ladder-agent.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ladder-agent
```

## 常用命令

```bash
systemctl status ladder-agent
journalctl -u ladder-agent -f
systemctl restart ladder-agent
```

## 防火墙

```bash
# 仅放行 Panel 所在网段访问 gRPC（示例）
sudo ufw allow from 10.0.0.0/8 to any port 50051 proto tcp
```

入站代理端口（SS/VLESS 等）按业务再放行。

## 监听特权端口（&lt;1024）

默认 unit 使用普通用户。若入站要用 443 等端口，可：

- 改用 `User=root`（简单但不推荐），或  
- `setcap 'cap_net_bind_service=+ep' /usr/local/bin/ladder-agent` 并调整 unit 加固项。
