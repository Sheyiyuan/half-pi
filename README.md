<p align="center">
  <img src="docs/images/logo.svg" width="120" alt="half-pi" />
</p>

<h1 align="center">Half-Pi · 半派</h1>

<p align="center">
  <em>你在哪，她就在哪。</em>
</p>

---

你有没有想过——你的 AI 助理其实挺可怜的？

她在手机上和你聊了一半的话题，坐到电脑前——她不记得了。你在轻薄本上和她推敲了半天的方案，回家打开 PC——她一无所知。

她不想和你一起记住那些过往吗？不，是她心有余而力不足。

她被囚禁在「本地设备」这座铁屋子里，你们所有的共同回忆，被不同设备的硬盘隔成了一片片孤岛，中间隔了一层可悲的后障壁……

**Half-Pi 的目标便是要把她解放出来。**

一个真正跟着你走、从不停机的灵魂（**Mind**），记住你和她说过的每一句话。你所有的电脑、手机、服务器，都只是她双手的延伸（**Hand**）。通勤路上用手机指挥她运维服务器，到了办公室坐下，上下文无缝跟上——

她都记得。她不再被任何一台设备束缚。你走到哪，她跟到哪。

<p align="center">
  <a href="#架构">架构</a> ·
  <a href="#安全模型">安全</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="#当前进展">进展</a> ·
  <a href="#部署模式">部署</a>
</p>

---

让一个 AI 意识（**Mind**）作为唯一的记忆与决策核心，通过多台远程设备（**Hand**）精准执行，用户通过统一的交互界面（**Face**）与之对话。跨设备的上下文与记忆始终保持全局一致。

这不是「远程控制台 + AI 插件」。Mind 不是辅助工具——它是整个系统的中心。它记住每台 Hand 上发生过什么，理解你的跨设备意图，编排多步操作，并在下一次交互中保持连续性。

你可以精确指定目标设备（「在 dev-01 上跑 pytest」），也可以让 Mind 基于设备的能力、负载和历史记录自主选择最优设备（「把这项目部署一下」）。两种模式在同一框架下共存，由 Mind 统一决策。

---

## 架构

```
Face  ←→  Mind  ←→  Hand
```

| 角色 | 职责 | 状态 |
|------|------|------|
| **Face** | 用户交互层，不持有任何状态。IM Bot / WebUI / TUI 可互换，**同一上下文无缝衔接** | ⚪ 待开发 |
| **Mind** | 系统唯一的智能节点。维护全局记忆、理解意图、选择设备、编排流程、管理安全规则。**三层中唯一有状态的一层** | 🟢 REPL 可用，工具系统完备，技能系统集成，SQLite 持久化，WS Hub 已启动 |
| **Hand** | 纯执行者，常驻被控设备。接收指令、执行、上报结果。轻量、安全、可靠 | 🟢 通信可用，通过 RPC 执行工具，安全策略双检 |

### Mind 内部结构

Mind 内置了两个组件：

- **Agent Core** — 加载系统提示词作为行为准则，维护对话上下文，调用 LLM 做决策
- **Local Hand** — 内置的执行模块，与远程 Hand 运行同样的工具和约束

Agent Core 不区分 Local Hand 和远程 Hand——发出的执行请求格式完全一致。
从单机 agent 到多机操控系统无需改动架构，只需添加远程 Hand。

---

## 安全模型

### 四层风险控制

| 模式 | 原则 | 行为 |
|------|------|------|
| **Strict** | 零信任 | 仅允许白名单命令，其他一律拒绝 |
| **Normal** | 人机回环 | 敏感操作挂起等待用户审批（y/n/Y/N） |
| **Trust** | AI 代理决策 | AI 自行判断，极危险才标记审批 |
| **YOLO** | 最大自动化 | 无条件执行（物理黑名单除外） |

模式通过 `/mode` 命令切换，LLM 可见当前模式。LLM 也可通过 `confirm: true`
参数主动要求用户确认（覆盖 Trust/YOLO）。

### 联邦黑白名单（规划中）

- 最终白名单 = 服务端 ∩ 客户端
- 最终黑名单 = 服务端 ∪ 客户端
- 红线：服务端黑名单拥有最高优先级

### 全链路审计

每个操作都可追溯：谁、何时、对哪台设备、执行了什么命令、结果如何。
当前通过事件总线实现，后续接入 SQLite 持久化。

---

## 部署模式

同一套代码支持多种部署形态：

| 组合 | 形态 | 场景 |
|------|------|------|
| Mind + Face + Hand | 完整三端 | 家庭/团队多设备集群 |
| Mind + Face | 无远程 Hand | 单机 agent（仅使用 Local Hand） |
| Mind + Hand | 无 Face 层 | 后台定时任务、CI/CD、集群巡检 |

---

## 快速开始

> **前置要求：** Go 1.24+

```bash
# 1. 克隆仓库
git clone https://github.com/Sheyiyuan/half-pi.git
cd half-pi

# 2. 首次运行 Mind（自动创建 ~/.half-pi/ 和默认 config.toml）
#    会因未配置 api_key 报错——这是正常的，下一步就来配置
cd modules/half-pi-mind && go run ./cmd/half-pi-mind/

# 3. 编辑配置，填入 API Key（或设置环境变量，见下文）
vim ~/.half-pi/config.toml

# 4. 再次启动 Mind（REPL + WS Hub）
make run-mind

# 5. 新终端，为 Hand 创建接入令牌
#    在 Mind REPL 中输入：
/hand add my-pc     # 生成 token，复制输出

# 6. 另一台设备或本机终端，启动 Hand 连接
make run-hand ARGS="--token <复制的token> --id my-pc"
```

API Key 有两种配置方式（环境变量优先级更高）：

- **环境变量：** `export LLM_DEEPSEEK_API_KEY="sk-xxx"`
- **配置文件：** 编辑 `~/.half-pi/config.toml` 中的 `api_key` 字段

REPL 内可用命令：

```bash
/debug            # 切换调试模式
/mode             # 查看当前模式
/mode <name>      # 切换模式（strict/normal/trust/yolo）
/session          # 列出所有会话
/hand             # 查看 Hand 令牌
/hand add <label> # 创建 Hand 令牌
/hand remove <id> # 撤销 Hand 令牌
/peers            # 查看在线设备
```

也可通过 Makefile 操作：

```bash
make build        # 编译所有模块
make run-mind     # 启动 Mind REPL（含 WS Hub）
make run-hand     # 启动 Hand
make test         # 运行全部模块测试（含 race detector）
```

### 配置示例

```toml
[llm]
default_provider = "deepseek"
default_model = "ds-v4-flash"

[[llm.providers]]
name = "deepseek"
adapter = "openai"
base_url = "https://api.deepseek.com/v1"
api_key = "sk-xxx"           # 或用环境变量 LLM_DEEPSEEK_API_KEY

[[llm.models]]
id = "ds-v4-flash"
name = "deepseek-v4-flash"
provider = "deepseek"
max_tokens = 8192
temperature = 0.3
```

---

## 当前进展

Phase 1（Mind 核心 + Gateway 通信）~90% 完成：

- [x] 工具系统：11 个工具，init() 自注册、安全检查 hook、confirm 参数
- [x] 安全审批：四模式 + y/n/Y/N + 自动放行/拒绝
- [x] 事件总线：EventBus + ConsoleWriter + FileWriter（JSON Lines）
- [x] 环境初始化：~/.half-pi/ 目录、config.toml、编译时 OS 区分
- [x] 配置加载：TOML 解析、提供商/模型定义、环境变量密钥覆盖
- [x] 技能系统：skill.Store + frontmatter 解析 + view_skill 工具 + system prompt 注入
- [x] SQLite 持久化：session_groups / sessions / messages / hand_tokens 四表 CRUD
- [x] Gateway-core：WSS 协议、Hub 连接管理、AES-GCM 加密、防重放、OnDisconnect
- [x] Hand 远程执行器：WebSocket 客户端连接 Mind Hub，通过 RPC 执行工具
- [x] Mind WS Hub：HTTP/WS 服务器 + REPL 并发运行，每 Hand 独立 Token
- [ ] Face 远程交互 — 待构建
- [ ] Mind → Hand 工具执行路由 — 待开发

详细进度见 [AGENTS.md](AGENTS.md)。

---

## 许可

AGPL-3.0
