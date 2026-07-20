# LadderAirport

自建代理机群控制面：Go Panel（内嵌 React + SQLite）+ 基于 [sing-box](https://github.com/SagerNet/sing-box) 的节点 Agent，通过 gRPC 批量管控。

```
浏览器 ──HTTP──► Panel ──gRPC──► Agent × N（进程内 sing-box）
```

## 功能

- **节点**：登记 / 探测 / 启停 / 远程升级，卡片与表格总览
- **入站模板**：SS / Trojan / VLESS(Reality) / Hysteria2 / TUIC / AnyTLS / VMess
- **配置下发**：关联入站 → 完整 sing-box JSON → gRPC 热更新；启动时自动同步并重试
- **订阅**：Clash / sing-box 链接，基础 CN 分流；可聚合外部机场订阅源
- **部署**：一键装成 systemd 服务

## 快速安装

无需克隆仓库，从 [GitHub Release](https://github.com/Jlan45/LadderAirport/releases) 拉最新二进制。

**Panel**

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-panel.sh \
  | sudo LADDER_SESSION_SECRET='请换成长随机串' bash
```

浏览器打开 `http://<host>:8080`，默认密码 `admin`（立刻改）。详见 [deploy/README-panel.md](deploy/README-panel.md)。

**Agent**

```bash
curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
  | sudo LADDER_TOKEN='请换成节点密钥' bash
```

默认开启 TLS。更推荐在 Panel「添加节点」生成一键安装命令。详见 [deploy/README-agent.md](deploy/README-agent.md)。

装完后：创建入站 → 关联到节点 → 下发。NAT 场景可在节点上拆分控制面地址与订阅公网地址。

## 本地开发

```bash
git clone --recurse-submodules https://github.com/Jlan45/LadderAirport.git
cd LadderAirport

make agent   # → bin/ladder-agent
make panel   # 构建 web 并 → bin/panel
make test
```

```bash
./bin/ladder-agent -listen 0.0.0.0:50051 -token test -data-dir /tmp/ladder-agent
./bin/panel -listen :8080 -db ./data/panel.db -session-secret 'dev-secret'
```

| 目录 | 作用 |
|------|------|
| `panel/` | 控制面 API、存储、转换器、gRPC 客户端 |
| `agent/` | `ladder-agent` + sing-box 子模块 |
| `web/` | React 前端（构建进 `panel/web/dist`） |
| `pkg/` `proto/` | 共享库与 gRPC 定义 |
| `scripts/` `deploy/` | 安装脚本与 systemd 单元 |

打 `v*` tag 会触发 Release 构建（linux/amd64 + arm64）。

## 安全

- 改掉默认管理员密码与节点 Token；Panel 用固定 `LADDER_SESSION_SECRET`
- 生产启用 Agent TLS，Panel 填入节点 CA；公网 Panel 建议反代 HTTPS
- 浏览器不直连 Agent，仅 Panel 访问控制口

## 许可证

Agent 运行时基于 [sing-box](https://github.com/SagerNet/sing-box) 上游协议。控制面代码为 monorepo 中独立部分。
