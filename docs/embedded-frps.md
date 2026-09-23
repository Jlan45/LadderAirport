# 节点内嵌 FRPS

LadderAirport Agent 将 FRP Server 作为 Go 库链接到进程中，不需要额外安装或由 systemd 管理 `frps` 二进制。FRPS 与 sing-box 使用独立运行时：任何一侧的配置下发、启停或错误都不会主动重启另一侧。

## 配置

打开节点详情的「FRPS」页签：

1. 开启 FRPS，点击「保存配置并自动启动」。
2. Panel 自动使用 `0.0.0.0:7000`、`20000-30000` 代理端口池、每客户端最多 8 个端口以及强制 TLS。
3. Panel 自动生成 256 位随机认证令牌并加密保存；页面默认隐藏，可按需查看。
4. 按页面给出的 FRPC 配置要点（服务端地址/端口、`auth.token`、TLS 开关、可用 `remote_port` 范围）配置客户端。在线 Agent 会立即启动 FRPS；Agent 重启后会恢复最后一次配置。

默认方案可直接使用。需要适配防火墙、NAT 或多网卡时，再展开「高级设置」覆盖绑定地址、控制端口、允许端口范围和客户端限额。

Panel 支持单独刷新状态、临时停止和重新启动。临时停止不会改变期望配置；若希望重启后仍保持停止，应关闭「启用 FRPS」并保存。

## 在线设备与映射

FRPS 运行后，「FRPS」页签展示在线 FRPC 设备、代理映射、本地服务地址、远端端口或域名、活动连接数与当日流量。数据在打开节点详情时实时拉取一次（FRPS 运行中自动加载），之后用「刷新在线设备」手动更新；这些瞬时数据不会写入 Panel 数据库。

每台 FRPC 设备应配置不同的 `clientID`（frpc 客户端配置字段，唯一标识一个 frpc 实例，缺省回退为 runID）；同一 `user` 下 `clientID` 重复的连接会被 FRPS 拒绝。token 可以共用，`clientID` 用于把映射准确归属到具体设备。

Agent 不会向公网开放上游 FRPS Dashboard。运行时会在 `127.0.0.1` 上创建随机端口和随机管理凭据，Panel 只能通过现有 Agent 安全 gRPC 查询经过整理的只读数据。

## 入站直连 FRP

先在「入站配置管理」创建可复用的协议入站，再到 Agent 管理页面打开目标节点的「入站」页签。关联入站时勾选「通过 FRP 暴露此入站」，直接粘贴服务商给出的完整 FRPC TOML 配置，然后保存并下发。每个入站只接受一条 `type = "tcp"` 的 `[[proxies]]`。同一个入站在不同 Agent 上可分别配置 FRP。Agent 会在进程内运行 FRPC，为该入站自动注册一条 TCP proxy；sing-box 仅在 `127.0.0.1` 的高位端口监听，FRPC 将工作连接转发到该端口，不对外开放本地入站端口。订阅使用配置中的 `serverAddr` 及 `remotePort`。

服务商配置中的 `user`、`auth.token`、`serverAddr/serverPort`、`transport.tls.*`、`[[proxies]].name` 和 `remotePort` 会被提取使用。`localIP/localPort` 可以保留在粘贴内容里，但 Ladder 会覆盖为 `127.0.0.1` 和自动分配的高位端口；无需把服务商示例中的内网 IP 调整为 Agent 地址。`# id` 是注释，不参与解析。其他暂不支持的字段会在保存时明确报错，不会被静默忽略。旧版逐项配置保存的 JSON 仍能解析，打开编辑时会转换为 TOML 草稿。

目前支持 TCP 入站，不支持 Hysteria2、TUIC、UDP-only 或额外的 WebSocket 等传输层。Shadowsocks 的订阅会关闭原生 UDP。每个映射的对外端口必须在 FRPS 允许范围内，且同一 FRPS 上不能与其他映射冲突。FRPS 控制端口和对外端口仍需对客户端可达。

Agent 入站关联中填写的 Token 副本保存在 Panel 数据库中，并随节点配置下发给 Agent；它不使用「FRPS」页签的加密字段。请将 Panel 数据库、配置快照及管理员访问权限视为敏感凭据。

## 安全边界

- FRPS 认证令牌使用 Panel 的 `secrets/credentials.key` 加密后写入 SQLite，HTTP API 和页面不会回显明文。
- token 可以供多个 FRPC 设备共用。普通配置接口不会携带明文，管理员点击「显示 Token」时才通过禁止缓存的独立接口解密返回。
- 主动轮换 token 后，必须同步更新所有使用旧 token 的 FRP 客户端。
- Agent 本地缓存包含 FRPS 运行所需的明文令牌，目录权限为 `0700`、文件权限为 `0600`。
- 管理面允许 `1-65535` 端口（含 1024 以下特权端口），要求 token 认证、心跳/工作连接认证和强制 TLS。绑定特权端口依赖 Agent 安装/升级时通过 setcap 与 systemd `AmbientCapabilities` 授予的 `cap_net_bind_service`；非 Linux 系统或缺少该 capability 时，绑定 1024 以下端口会在运行期失败。
- `allow_ports` 是强制配置项。只开放实际需要的端口范围，并配合主机防火墙限制 FRPS 控制端口来源。
- `proxy_bind_addr` 决定代理端口监听的网卡；若无需公网监听，使用内网或回环地址。

FRP 客户端配置中的 `server_addr` / `server_port` 应指向节点对外可达的 FRPS 控制地址，`auth.token` 必须与 Panel 中保存的令牌一致。

## 升级与兼容

支持该功能的 Agent 在 `Ping` 中报告 `frps-v1`、`frps-mappings-v1` 能力和内嵌 FRPS 版本。Panel 已经确认节点不具备对应能力时会阻止下发或提示升级。FRP 源码固定在 `agent/frp` 子模块的稳定标签，升级时应同时验证 Agent 单元测试、跨平台构建和 FRP 客户端连通性。
