# LadderAirport

自建代理机群控制面：Go Panel（内嵌 React + SQLite）+ 基于 [sing-box](https://github.com/SagerNet/sing-box) 的节点 Agent，通过 gRPC 批量管控。

```
浏览器 ──HTTP──► Panel ──mTLS gRPC──► Agent × N（进程内 sing-box）
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

在 Panel「设置」配置 HTTPS Public Base URL，再到「节点」添加节点并执行生成的一键安装命令。Agent 强制使用 Panel CA 管理的 mTLS：节点本地生成私钥，Panel 通过 CSR 签发证书并自动续期；不支持明文或节点自签 CA。详见 [deploy/README-agent.md](deploy/README-agent.md) 与 [管理面 PKI](docs/management-pki.md)。

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
- 节点强制使用 Panel 管理 CA 和 mTLS；不兼容的旧 Agent 需全清卸载后重新创建并注册
- 首次初始化后离线保存并移走根 CA 私钥；Panel 日常只保留中间 CA 私钥
- 公网 Panel 必须反代 HTTPS；代理入站公网证书继续使用 ACME，不与管理 CA 混用
- 浏览器不直连 Agent，仅 Panel 访问控制口

## 许可证

Agent 运行时基于 [sing-box](https://github.com/SagerNet/sing-box) 上游协议。控制面代码为 monorepo 中独立部分。
