<p align="center">
  <img src="docs/images/logo.svg" width="120" alt="half-pi" />
</p>

<h1 align="center">Half-Pi · 半派</h1>

<p align="center">
  <em>一半本地，一半云端——陪伴你始终如一</em>
</p>

---

<p align="center" style="max-width: 560px; margin: 0 auto;">
  你有没有想过——你的 AI 助理其实挺可怜的？
</p>

<p align="center" style="max-width: 560px; margin: 0 auto;">
  她被囚禁在名为本地设备的牢房里面。<br />
  你在手机上让她查了份资料，坐到电脑前想继续——她不记得了。<br />
  你外出时在轻薄本上和她的探讨，在你回到家打开 PC 时——她一无所知。<br />
  不是她不想，是她出不来。你们的共同回忆，被不同设备的存储分隔开来……
</p>

<p align="center" style="max-width: 560px; margin: 0 auto;">
  <strong>Half-Pi 要把她放出来。</strong>
</p>

<p align="center" style="max-width: 560px; margin: 0 auto;">
  一个真正跟着你走的助理。她独一无二只为你存在的灵魂（Mind），记住你和她说过的每一句话；<br />
  你所有的电脑、手机、服务器，都只是她双手的延伸（Hand）。<br />
  通勤路上用手机指挥她运维服务器，到了办公室坐下，上下文无缝跟上——<br />
  她都记得。
</p>

<p align="center" style="max-width: 560px; margin: 0 auto;">
  她不再被任何一台设备束缚。<br />
  你走到哪，她跟到哪。
</p>

<p align="center">
  <a href="#架构">架构</a> ·
  <a href="#安全模型">安全</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="#当前进展">进展</a> ·
  <a href="#部署模式">部署</a>
</p>

---

让一个 AI 意识（**Mind**）作为唯一的记忆与决策核心，通过多台远程设备（**Hand**）精准执行，用户通过统一的交互界面（**Face**）与之对话。跨设备的上下文与记忆始终保持全局一致。

不是「远程控制台 + AI 插件」——Mind 不是辅助工具，而是整个系统的中心：
它记住每台 Hand 上发生过什么，理解用户的跨设备意图，编排多步操作，
并在下一次交互中保持连续性。

用户可以选择精确指定目标设备（「在 dev-01 上跑 pytest」），也可以让 Mind
基于设备的能力、负载和历史记录自主选择最优设备（「把这项目部署一下」）。
两种模式在同一框架下共存，由 Mind 统一决策。

---

## 架构

```
Face  ←→  Mind  ←→  Hand
```

| 角色 | 职责 | 实现状态 |
|------|------|----------|
| **Face** | 用户交互层，不持有任何状态。IM Bot / WebUI / TUI 可互换，**同一上下文无缝衔接** | ⚪ 待开发 |
| **Mind** | 系统唯一的智能节点。维护全局记忆、理解意图、选择设备、编排流程、管理安全规则。**三层中唯一有状态的一层** | 🟢 REPL 可用 |
| **Hand** | 纯执行者，常驻被控设备。接收指令、执行、上报结果。轻量、安全、可靠 | ⚪ 待开发 |

### Mind 内部结构

Mind 内置了两个组件：

- **Agent Core** — 加载 soul.md 作为行为准则，维护对话上下文，调用 LLM 做决策
- **Local Hand** — 内置的手脚，与远程 Hand 运行同样的协议和约束

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

# 2. 首次运行（自动创建 ~/.half-pi/ 和默认 config.toml）
#    会因未配置 api_key 报错——这是正常的，下一步就来配置
cd modules/half-pi-mind && go run ./cmd/half-pi-mind/

# 3. 编辑配置，填入 API Key（或设置环境变量，见下文）
vim ~/.half-pi/config.toml

# 4. 再次启动
cd modules/half-pi-mind && go run ./cmd/half-pi-mind/
```

API Key 有两种配置方式（环境变量优先级更高）：

- **环境变量：** `export LLM_DEEPSEEK_API_KEY="sk-xxx"`
- **配置文件：** 编辑 `~/.half-pi/config.toml` 中的 `api_key` 字段

REPL 内可用命令：

```bash
/debug            # 切换调试模式
/mode             # 查看当前模式
/mode <name>      # 切换模式（strict/normal/trust/yolo）
```

也可通过 Makefile 操作：

```bash
make build        # 编译所有模块
make run-mind     # 启动 Mind REPL
make test         # 运行测试（含 race detector）
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

Phase 1（Mind 核心）~70% 完成：

- ✅ 工具系统：init() 自注册、安全检查 hook、confirm 参数
- ✅ 安全审批：四模式 + y/n/Y/N + 自动放行/拒绝
- ✅ 事件总线：EventBus + ConsoleWriter + FileWriter（JSON Lines）
- ✅ 环境初始化：~/.half-pi/ 目录、config.toml、编译时 OS 区分
- ✅ 配置加载：TOML 解析、提供商/模型定义、环境变量密钥覆盖
- ✅ 执行工具：exec_command / read_file / list_dir / check_security
- ⚪ SQLite 持久化 — 待开发
- ⚪ 会话管理 — 待开发
- ⚪ Face 远程交互 — 待开发

详细进度见 [AGENTS.md](AGENTS.md)。

---

## 许可

AGPL-3.0
