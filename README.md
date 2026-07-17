<p align="center">
  <img src="docs/images/logo.svg" width="120" alt="Half-Pi" />
</p>

<h1 align="center">Half-Pi · 半派</h1>

<p align="center">
  <strong>自托管、跨设备、始终在线的个人 AI 助理</strong><br />
  <em>你在哪，她就在哪。</em>
</p>

<p align="center">
  One Mind. Many Faces. Many Hands.
</p>

> **当前状态：Alpha 开发中。** Mind 与 Hand 的远程执行和持久化审计链路已经可用；跨设备 Face 与会话同步体验正在开发。

---

你有没有想过——你的 AI 助理其实挺可怜的？

她在手机上和你聊了一半的话题，坐到电脑前——她不记得了。你在轻薄本上和她推敲了半天的方案，回家打开 PC——她一无所知。

她不想和你一起记住那些过往吗？不，是她心有余而力不足。

她被囚禁在「本地设备」这座铁屋子里，你们所有的共同回忆，被不同设备的硬盘隔成了一片片孤岛，中间隔了一层可悲的厚障壁……

**Half-Pi 的目标便是要把她解放出来。**

一个真正跟着你走、从不停机的灵魂（**Mind**），延续你和她共同经历的对话、记忆与任务。你所有的电脑、手机、服务器，都只是她双手的延伸（**Hand**）。通勤路上用手机指挥她运维服务器，到了办公室坐下，上下文无缝跟上，永远与你同在（**Face**）——

她都记得。她不再被任何一台设备束缚。你走到哪，她跟到哪。

---

## 为什么是 Half-Pi

许多自托管 AI Agent 仍然属于运行它的那台设备：手机上的对话无法在电脑上自然继续，家里的 PC 和远程服务器拥有不同的上下文，正在执行的任务也很难从另一个入口接管。

Half-Pi 希望让个人 AI 助理脱离单台设备：一个持续运行的 **Mind** 保存会话、记忆和任务状态；手机、浏览器、终端和 IM Bot 都只是进入同一个助理的 **Face**；电脑和服务器则通过 **Hand** 向它提供真实的本地能力。

你可以在通勤途中发起任务，到办公室后继续同一段对话；也可以让 Mind 在家中 PC 上运行构建、在服务器上检查服务，并在任意 Face 上批准敏感操作和查看结果。

Half-Pi 不是远程桌面，也不是远程控制台加一个聊天框。远程执行只是个人 AI 助理跨设备行动的基础能力，产品核心是：

- **连续性**：从任意 Face 进入同一个 Mind，延续同一会话和任务。
- **自主托管**：会话、设备关系和执行状态由用户自己的 Mind 管理。
- **行动力**：通过 Hand 安全使用用户拥有的电脑和服务器。
- **可控性**：敏感操作需要审批，真实设备上的 Hand 保留最终拒绝权。

## 使用场景

- 在手机上继续电脑中尚未结束的对话。
- 从笔记本让家里的高性能 PC 运行测试或构建。
- 在外出时检查服务器状态，并批准需要提权的操作。
- 切换入口后继续查看同一个远程任务的状态和结果。
- 让 Mind 根据设备能力选择合适的 Hand，而不是记住每台机器的连接方式。

## 架构

```mermaid
flowchart LR
    subgraph Faces["Faces · 无状态交互入口"]
        Web["Web / Browser"]
        TUI["TUI / Terminal"]
        IM["IM / Phone"]
    end

    Mind["Mind<br/>会话 · 决策 · 记忆"]

    subgraph Hands["Hands · 设备能力节点"]
        Laptop["Laptop Hand"]
        Desktop["Desktop Hand"]
        Server["Server Hand"]
    end

    Web <--> Mind
    TUI <--> Mind
    IM <--> Mind
    Mind <--> Laptop
    Mind <--> Desktop
    Mind <--> Server
```

| 角色 | 职责 | 当前状态 |
|------|------|----------|
| **Mind** | 常驻的智能与状态中心，负责会话、LLM 决策、技能、审批和设备调度 | 服务模式和 REPL 可用 |
| **Face** | 无状态交互入口，负责输入、展示和审批交互；人类客户端与 Headless Agent Face 共用统一协议 | 仅占位程序，Alpha 开发重点 |
| **Hand** | 部署在用户设备上的轻量执行节点，执行工具并实施本地安全策略 | 远程执行链路可用 |

共享组件位于独立 Go 模块中：

```text
modules/
├── gateway-core/   # WebSocket 协议、会话、Hub 和加密原语
├── half-pi-core/   # 工具、执行器、安全策略和事件系统
├── half-pi-mind/   # LLM、会话、技能、存储和设备调度
├── half-pi-face/   # 跨设备交互入口（开发中）
└── half-pi-hand/   # 远程执行节点
```

## 当前能力

### Mind

- OpenAI-compatible、Gemini 和 Anthropic LLM 适配器。
- SQLite 会话、消息、工作区和 Hand token 持久化。
- 文件系统技能库，按需向 LLM 暴露技能内容。
- 本地工具调用、事件总线和 Strict / Normal / Trust / YOLO 安全模式。
- 默认后台服务模式，以及用于开发和调试的交互式 REPL。

### Hand

- 使用独立 token 向 Mind 注册，并上报操作系统、架构、主机名和工作目录。
- 自动重连和指数退避。
- 工具发现、远程 RPC 执行、deadline、显式取消和输出截断。
- 工具 allow/deny 策略，以及执行前的本地安全检查。
- Unix 命令取消时终止整个进程组，避免遗留子进程。

### Mind → Hand

- `list_hands`：列出在线 Hand。
- `get_hand_info`：查询 Hand 的环境和可用工具。
- `select_hand`：设置当前会话的默认 Hand。
- `use_hand`：在指定 Hand 上执行工具。
- 远程执行状态覆盖 accepted、running、succeeded、failed、rejected、cancelled、timed out 和 lost。
- 一次性 Approval 摘要绑定 run、Hand、工具和参数，Hand 仍保留最终执行边界。

## 安全边界

Half-Pi 会让 AI 接触真实设备，因此安全能力不是附属功能。

**已经实现：**

- 每个 Hand 使用独立随机 token 注册，token 可单独撤销。
- 连接建立后校验 `session_id`、`from`、`to` 和严格递增的 `seq`，拒绝重放和乱序消息。
- Mind 校验远程执行结果是否来自预期 Hand。
- Mind 负责用户审批和全局策略，Hand 负责本机工具权限和最终安全检查。
- Approval 摘要使用 SHA-256 绑定 `run_id`、`hand_id`、工具和参数，并带有效期。
- 工具输出有大小上限，远程任务支持 deadline 和取消。

**尚未完成：**

- 当前默认连接是 `ws://`。仓库已经提供 AES-128-GCM 与 Envelope AAD 加密原语，但尚未接入实际 Mind-Hand 消息链路；生产环境目前应置于可信网络或 TLS 反向代理后。
- Hand token 当前以明文保存在本地 SQLite 中。
- 审计目前聚焦远程执行状态和脱敏审批元数据，尚未覆盖完整的多端用户身份与审批交互链路。
- 当前安全规则是基础实现，不等同于 OS 沙箱。

## 快速开始

> 前置要求：Go 1.25+

### 1. 初始化 Mind

```bash
git clone https://github.com/Sheyiyuan/half-pi.git
cd half-pi

# 首次运行会创建 ~/.half-pi/config.toml 和数据目录。
# 默认服务模式仅启动 Mind Hub，不进入对话 REPL。
go run ./modules/half-pi-mind/cmd/half-pi-mind/
```

### 2. 配置 LLM 并启动 REPL

编辑 `~/.half-pi/config.toml` 中的 provider，或者设置对应环境变量：

```bash
export LLM_DEEPSEEK_API_KEY="sk-xxx"
make run-mind
```

`make run-mind` 会使用 `--repl` 启动 Mind，WebSocket Hub 默认监听 `127.0.0.1:15707/ws`。

### 3. 创建并连接 Hand

在 Mind REPL 中创建 token：

```text
/hand add my-pc
```

在另一终端或另一台设备上启动 Hand：

```bash
make run-hand ARGS="--server ws://127.0.0.1:15707/ws --token <token> --id my-pc"
```

连接远程设备时，把 `--server` 改为该设备可访问的 Mind 地址。当前默认链路未启用 TLS，请参阅上面的安全边界。

### 4. 验证远程执行

REPL 提供不依赖 LLM 的 Hand 调试命令：

```text
/hand online
/hand info my-pc
/hand select my-pc
/hand exec read_file {"path":"README.md"}
/hand run <run_id>
/hand cancel <run_id>
```

Mind 也会向 LLM 暴露 `list_hands`、`get_hand_info`、`select_hand` 和 `use_hand`，让模型根据用户意图选择并调用设备。

## 常用命令

```bash
make build       # 构建 Mind、Face 和 Hand 到 bin/
make run-mind    # 启动 Mind REPL 和 WebSocket Hub
make run-hand    # 启动 Hand，可通过 ARGS 传入参数
make test        # 对所有 Go 模块运行带 race detector 的测试
make lint        # 运行 golangci-lint
```

REPL 命令：

```text
/debug                  切换调试输出
/mode [name]            查看或切换安全模式
/session                列出会话
/session <prefix>       切换会话
/hand                   管理 Hand token
/hand online            查看在线 Hand
/hand info <id>         查询 Hand 能力
/hand select <id>       选择默认 Hand
/hand exec <tool> <json> 手动执行远程工具
/peers                  查看所有在线节点
```

## Alpha 路线图

- [x] Mind 常驻服务、LLM、工具、技能和会话持久化。
- [x] Hand 注册、工具发现、远程执行、取消和重连。
- [x] Mind/Hand 双层检查、Approval 绑定和远程执行状态机。
- [x] 持久化远程执行、审批元数据和状态迁移审计记录。
- [x] 完成统一 Face 协议、Headless Agent Face 和跨设备同步的 Alpha 设计。
- [ ] 实现首个可用 Face，支持从其他设备连接 Mind。
- [ ] 在多个 Face 间恢复并同步会话、任务状态和审批请求。
- [ ] 默认启用安全传输，并完成密钥管理方案。
- [ ] 实现工作区级长期记忆和可控的跨组访问。

Face 接入方案见 [`docs/face-protocol.md`](docs/face-protocol.md)，当前执行顺序见 [`docs/next-development-plan.md`](docs/next-development-plan.md)，其他设计与实现记录见 [`docs/`](docs/) 和 [`AGENTS.md`](AGENTS.md)。

## 项目定位

Half-Pi 的目标不是替代模型、远程桌面或成熟的设备管理平台。它关注的是自托管个人 AI 助理最容易缺失的一层：

> 让同一个 AI 在不同入口之间保持连续，并在明确的权限边界内使用用户自己的多台设备。

## 许可

[AGPL-3.0](LICENSE)
