# 管理面 PKI

LadderAirport 的管理 PKI 只保护 Panel 到 Agent 的 gRPC 控制面。代理入站的公网域名证书仍应使用 ACME，不能把管理根 CA 当作公网 CA 使用。

## 证书层级

首次启动 Panel 时，默认在 `<数据库目录>/pki` 创建：

- `root-ca.crt`：10 年根 CA 证书；
- `offline/root-ca.key`：根 CA 私钥，只用于签发/轮换中间 CA；
- `intermediate-ca.crt`、`intermediate-ca.key`：2 年在线中间 CA；
- `panel-client.crt`、`panel-client.key`：Panel 的 mTLS 客户端身份；
- Agent 证书：30 天，有效期经过约三分之二（剩余约三分之一）时自动续签。

Panel 日常签发 Agent 证书不需要根 CA 私钥。完成首次初始化并确认备份后，应停止 Panel、将 `offline/root-ca.key` 转移到加密离线介质，再启动 Panel。需要轮换中间 CA 时临时恢复该文件并执行：

```bash
ladder-panel -db /var/lib/ladder-airport/panel.db -pki-rotate-intermediate
```

命令会轮换在线中间 CA 与 Panel 客户端证书，然后退出。完成后再次移走根私钥并正常启动 Panel。根信任锚不变，因此已有 Agent 证书在其剩余有效期内仍可验证。

目录可通过 `-pki-dir` 指定。管理 PKI 是必需组件，Panel 缺少完整的根证书、在线中间 CA 或中间 CA 私钥时会拒绝启动。私钥文件以 `0600` 创建，PKI 目录应只允许 Panel 服务账号访问。

## 新节点注册

Panel 创建节点时会生成一个 15 分钟有效、只能使用一次的注册令牌。安装流程如下：

1. Agent 本地生成 ECDSA P-256 私钥和带 DNS/IP SAN 的 CSR；
2. 使用一次性令牌请求 `POST /api/v1/pki/agent-certificates`；
3. Panel 校验令牌、签发带 `spiffe://ladderairport/agent/<node-id>` 身份的证书；
4. Panel 返回证书链、CA 信任包和长期 Agent 控制令牌；
5. 安装脚本以原子方式写入证书与 CA 信任包；Agent 启动后强制 mTLS，只接受持有有效 Panel 客户端证书（`spiffe://ladderairport/panel/control`）的连接。

私钥不会离开 Agent。节点证书目录仅允许 Agent 服务账号访问，以便原子续签；私钥文件权限为 `0600`。Panel 连接时同时校验证书链、节点 URI 身份和当前绑定的证书序列号；仅有其他节点的合法证书也无法冒充目标节点。

## 续签和热加载

Agent 每 6 小时检查证书，在有效期经过约三分之二后用现有私钥生成新 CSR。续签使用 Agent 控制令牌。新证书写入后无需重启 Agent，新的 TLS 握手即读取最新证书。CA 信任包按文件修改时间缓存、变更即重新加载，为重叠信任的 CA 轮换保留基础。

续签失败不会立即替换旧证书，Agent 会保留仍然有效的证书并在下次周期重试。建议监控「证书」页面的 7 天内到期数量。

## 吊销与审计

管理员可在「证书」页面吊销当前证书。吊销会：

- 将证书台账状态改为 `revoked`；
- 解除节点与证书序列号的绑定，并清空节点控制令牌；
- 把节点标记为 `unauthorized`；
- 写入 PKI 审计日志。

Panel 随后不会再使用该身份连接节点。要恢复节点，应重新生成安装命令并完成注册。短生命周期证书用于限制离线节点无法立即接收吊销信息时的风险窗口。

## 备份

至少备份：

- `root-ca.crt`；
- 离线的 `root-ca.key`；
- `intermediate-ca.crt` 和 `intermediate-ca.key`；
- Panel SQLite 数据库。

根 CA 私钥和数据库备份应分开保存。泄露中间 CA 私钥时，应恢复离线根密钥签发新中间 CA，发布包含新旧信任锚的过渡信任包，完成 Agent 续签后移除旧 CA。
