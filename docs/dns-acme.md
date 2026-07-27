# DNS / ACME 运维指南

LadderAirport 可由 Panel 调用 DNS API 维护节点 A/AAAA 记录，并通过 ACME DNS-01 为 Trojan、VLESS TLS、Hysteria2、TUIC、AnyTLS 和 VMess TLS 签发公网证书。

管理面 mTLS CA 与协议证书相互独立：

- Panel CA 只验证 Panel ↔ Agent 管理连接。
- ACME 证书只用于用户连接代理入站。
- 协议私钥在 Agent 本地生成，Panel 只取得 CSR、公开指纹和证书链。

## 首次配置

1. 备份 Panel 数据库和凭据主密钥。默认密钥位于数据库同级的 `secrets/credentials.key`，权限为 `0600`。也可通过 `LADDER_CREDENTIALS_KEY=hex:<64位十六进制>` 注入。
2. 在「DNS/ACME → DNS 账号」添加供应商账号并测试连接。
3. 添加托管域名，等待状态变为「就绪」。
4. 添加 ACME 账号并明确接受服务条款。建议先用 Let's Encrypt Staging：
   `https://acme-staging-v02.api.letsencrypt.org/directory`
5. 注册 ACME 账号并申请协议证书。
6. 证书变为「有效」后，到节点详情的入站页将 TLS 模式切换为「托管 TLS」。

配置预览、正式下发、订阅和代理链下一跳均使用同一套托管 TLS 解析。托管模式会使用域名作为客户端地址/SNI，并保持证书校验开启。

## DNS 权限

建议为 Panel 创建最小权限凭据，只允许操作目标区域中的 A、AAAA、TXT：

- AliDNS：AccessKey ID / Secret，推荐使用 RAM 子账号或 STS。
- DNSPod：SecretId / SecretKey，推荐使用受限子账号。
- Cloudflare：API Token 至少需要目标 Zone 的 DNS Edit 和 Zone Read。
- Callback：默认只允许 HTTPS、公网目标和无重定向响应；访问内网 Callback 必须显式设置 `allow_private_network: true`。

供应商设置中的 `test_zone` 用于连接测试。Callback 设置示例：

```json
{
  "url": "https://dns-api.example.net/records",
  "method": "POST",
  "headers": {
    "Authorization": "Bearer #{credential.token}"
  },
  "body": "{\"action\":\"#{action}\",\"zone\":\"#{zone}\",\"name\":\"#{name}\",\"type\":\"#{type}\",\"value\":\"#{value}\",\"ttl\":\"#{ttl}\",\"id\":\"#{id}\"}",
  "success_statuses": [200, 201, 204],
  "test_zone": "example.com"
}
```

查询动作应返回：

```json
{
  "records": [
    {"id":"provider-record-id","name":"edge","type":"A","value":"203.0.113.10","ttl":300000000000}
  ]
}
```

`ttl` 在 Callback JSON 响应中使用 Go `time.Duration` 纳秒值；请求模板里的 `#{ttl}` 使用秒。

## 任务与故障恢复

DNS 对账、记录清理、签发和续期都是 SQLite 持久化任务：

- Panel 重启后会接管租约过期的任务。
- 同一目标同一类型只保留一个活动任务。
- DNS 与 ACME 外部调用不在数据库事务中执行。
- DNS-01 只清理本次挑战的精确 TXT 值，不会删除同名的其他验证记录。
- 续期失败时继续保留上一个有效证书和 Agent 证书代次。

「DNS/ACME → 任务」会显示尝试次数、重试时间和中文错误。供应商密钥、EAB HMAC 和 ACME 账号密钥不会出现在 API 响应或任务错误中。

## 删除与恢复

- 删除 Panel 创建的 A/AAAA 记录必须显式确认 `delete_records=true`；接管的已有记录不会自动删除。
- 已被 TLS 入站引用的协议证书不能删除，应先切回传统 TLS。
- 删除 DNS 或 ACME 账号前必须解除关联资源。
- 丢失 `credentials.key` 后，现有加密凭据不可恢复；从同一时间点的数据库和密钥备份一起恢复，或重新录入全部账号凭据。

生产切换前应在 Staging 完成至少一次：DNS 对账、签发、节点下发、订阅连接和自动续期演练。
