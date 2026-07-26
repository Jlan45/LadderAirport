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

## 旧节点一次性迁移

在旧节点详情复制「一次性 PKI 迁移命令」并以 root 执行。迁移脚本会在旧服务仍运行时完成 CSR 签发和新二进制准备，切换后连续检查服务状态；失败会在本次执行内恢复旧服务，成功后会删除临时回滚、旧 CA 私钥、旧 unit 残留和旧二进制备份。

迁移成功后旧管理面实现不再可用，也不应再次运行旧安装脚本。若要使用本地预构建的新 Agent：

```bash
sudo env \
  LADDER_PANEL='https://panel.example.com' \
  LADDER_NODE_ID='<node-id>' \
  LADDER_ENROLL_TOKEN='<one-time-token>' \
  LADDER_AGENT_BINARY=/path/to/ladder-agent \
  ./scripts/migrate-agent-to-panel-pki.sh
```

也可以从同一 Release 获取脚本和 Agent 二进制：

```bash
curl -fsSL https://github.com/Jlan45/LadderAirport/releases/download/vX.Y.Z/migrate-agent-to-panel-pki.sh \
  | sudo env \
      LADDER_VERSION=vX.Y.Z \
      LADDER_PANEL='https://panel.example.com' \
      LADDER_NODE_ID='<node-id>' \
      LADDER_ENROLL_TOKEN='<one-time-token>' \
      bash
```

## NAT / 端口转发

Panel 主动拨号 Agent gRPC。Agent 位于 NAT 后时，应通过 VPN、DNAT 或端口映射让 Panel 可达：

```text
[客户端] --入站端口--> [公网 IP / DDNS] --DNAT--> [Agent 入站]
[Panel]  --mTLS gRPC-> [VPN 或映射的 gRPC] -----> [Agent :50051]
```

- `address` / `grpc_port`：Panel 能拨到的控制面地址和外部映射端口；
- `public_address`：订阅客户端使用的默认公网地址；
- 入站关联的 `public_address` / `public_port`：单个入站的 NAT 覆盖；
- `LADDER_REPORT_ADDRESS`：首次注册时上报的控制面地址；
- `LADDER_TLS_EXTRA_SANS`：额外证书 SAN，例如 `DNS:node.example.com,IP:203.0.113.10`。

## 升级

已完成 Panel PKI 注册的节点可以在 Panel 中远程升级，也可以执行：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo env LADDER_ACTION=upgrade LADDER_VERSION=v0.9.0 bash
```

普通升级只替换二进制并保留 Panel PKI 身份。若检测到旧节点 CA 或缺少 mTLS 参数，升级会拒绝执行并提示使用一次性迁移脚本。

## 常用命令

```bash
systemctl status ladder-agent
journalctl -u ladder-agent -f
systemctl restart ladder-agent
```

卸载但保留配置和数据：

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo env LADDER_ACTION=uninstall bash
```

加上 `LADDER_PURGE=1` 会同时删除配置、证书和数据目录。

## 防火墙与权限

只允许 Panel 来源访问 Agent gRPC 端口；代理入站端口按业务放行。默认 unit 使用 `ladder` 用户。监听 1024 以下端口时，可授予二进制 `cap_net_bind_service`，不建议改为 root 常驻。
