# 快速部署 Agent（systemd）

Agent 管理面只支持 Panel CA 签发的双向 TLS，不支持明文、节点自签 CA 或手工粘贴 CA。

## 新节点安装

先在 Panel「设置」配置 HTTPS 的 Public Base URL，再在「节点」添加节点并复制一键安装命令。命令包含 15 分钟有效、只能使用一次的注册令牌。

安装过程中：

1. 节点本地生成 ECDSA P-256 私钥与 CSR；
2. Panel CA 签发带节点 URI 身份的 30 天证书；
3. 安装脚本写入严格 mTLS 配置并启动 systemd 服务；
4. Agent 在证书有效期经过约三分之二后自动续签。

私钥始终留在节点。不要单独执行不含 `LADDER_PANEL`、`LADDER_NODE_ID` 和 `LADDER_ENROLL_TOKEN` 的安装命令。

指定版本时，可在 Panel 生成命令时填写版本，或在命令的 `sudo env` 后增加 `LADDER_VERSION=v0.9.0`。

## 控制令牌

Agent 控制令牌由 Panel 在注册时签发随机值，安装脚本写入 `/etc/ladder-agent/agent.env` 的 `LADDER_TOKEN`，经 unit 的 `EnvironmentFile` 注入环境变量，不出现在命令行（避免 `ps` 泄露）。Agent 拒绝空值与弱默认值 `changeme`。

## 系统指标与 BBR

节点详情「系统状态」页签可查看 CPU / 内存 / 磁盘 / 网卡实时速率，并开关 BBR 拥塞控制（需 Agent 上报 `node-metrics-v1` / `bbr-v1` 能力，旧版本 Agent 会提示先升级）。

Web 端开关即可，无需登录节点操作。原理：Agent 以非特权用户运行，不直接改内核参数；点击开关后 Agent 在数据目录落盘 `bbr.request`（内容为 `enable` / `disable`），由安装脚本一并安装的 root helper 执行——`ladder-agent-bbr.path` 监视该文件，`ladder-agent-bbr.service`（oneshot，root）随即应用：

- **启用**：`sysctl -w net.core.default_qdisc=fq net.ipv4.tcp_congestion_control=bbr`，并写入 `/etc/sysctl.d/99-ladder-bbr.conf` 持久化（重启后保持）；
- **关闭**：`sysctl -w net.ipv4.tcp_congestion_control=cubic` 恢复默认，并删除该 conf。

运维排障：`systemctl status ladder-agent-bbr.path`、`journalctl -u ladder-agent-bbr.service`。卸载默认保留该 conf，`LADDER_PURGE=1` 全清时一并删除。

## HTTP 上行（uplink）

节点无法被 Panel 拨到时，创建或切换为 `control_mode=uplink`。安装命令会写入 `LADDER_UPLINK=1`。这类节点不初始化管理面 TLS：安装脚本调用 `POST /api/v1/agent/enroll` 换取控制令牌，之后用 HTTP 上报，并用 WebSocket 长连接收配置。不新开端口。

uplink 节点默认**不监听 gRPC 控制端口**。若该节点其实公网可达、或想保留被 push 拨号的能力，设 `LADDER_UPLINK_SERVE_GRPC=1`，安装会改回完整 PKI / mTLS。详见 [Agent 上行](../docs/agent-uplink.md)。

## NAT / 端口转发

push 模式由 Panel 主动拨号 Agent gRPC。Agent 位于 NAT 后又必须走 push 时，应通过 VPN、DNAT 或端口映射让 Panel 可达：

```text
[客户端] --入站端口--> [公网 IP / DDNS] --DNAT--> [Agent 入站]
[Panel]  --mTLS gRPC-> [VPN 或映射的 gRPC] -----> [Agent :50051]
```

- `address` / `grpc_port`：Panel 能拨到的控制面地址和外部映射端口；
- `public_address`：订阅客户端使用的默认公网地址；
- 入站关联的 `public_address` / `public_port`：单个入站的 NAT 覆盖；
- `LADDER_REPORT_ADDRESS`：首次注册时上报的控制面地址；
- `LADDER_TLS_EXTRA_SANS`：额外证书 SAN，例如 `DNS:node.example.com,IP:203.0.113.10`；
- `LADDER_IP_ECHO_URLS`：Agent 进程的公网 IP 探测源，逗号分隔、每项为返回纯文本 IP 的 URL；非空时覆盖默认源（`api4/api6.ipify.org`、`ipv4/ipv6.icanhazip.com`、`v4/v6.ident.me`）。自定义列表同时用于 IPv4/IPv6 探测（响应族别不符的源自动跳过），仍要求至少 2 个源结果一致。注意与安装脚本的 `LADDER_IP_ECHO_URL` 无关——后者只影响脚本安装时的自身探测（可设空关闭），不写入 Agent 运行环境。

## 升级

已完成 Panel PKI 注册的节点可以在 Panel 中远程升级，也可以执行：

```bash
curl -fsSL https://raw.githubusercontent.com/LadderAirport/LadderAirport/main/scripts/install-agent.sh \
  | sudo env LADDER_ACTION=upgrade LADDER_VERSION=v0.9.0 bash
```

普通升级只替换二进制。push 节点以及打开了 `LADDER_UPLINK_SERVE_GRPC` 的节点仍保留 Panel PKI 身份；检测到自签 CA 或缺少 mTLS 材料时升级会拒绝。默认 uplink（HTTP + WebSocket、不监听 gRPC）不要求管理面证书，升级不会因为没有 TLS 文件而失败。

## 常用命令

```bash
systemctl status ladder-agent
journalctl -u ladder-agent -f
systemctl restart ladder-agent
```

卸载但保留配置和数据：

```bash
curl -fsSL https://raw.githubusercontent.com/LadderAirport/LadderAirport/main/scripts/install-agent.sh \
  | sudo env LADDER_ACTION=uninstall bash
```

加上 `LADDER_PURGE=1` 会同时删除配置、证书和数据目录。

## 防火墙与权限

只允许 Panel 来源访问 Agent gRPC 端口；代理入站端口按业务放行。默认 unit 使用 `ladder` 用户。安装脚本默认即授予二进制 `cap_net_bind_service`（`setcap cap_net_bind_service+ep`，并在 unit 中配置 `AmbientCapabilities=CAP_NET_BIND_SERVICE`，远程升级后也会重新授予），1024 以下端口可直接监听，无需 root 常驻。
