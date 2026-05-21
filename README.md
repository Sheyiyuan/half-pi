# half-pi

> half cloud, half local — a self-improving coding agent

half-pi 是一个自我迭代的 CLI Agent 框架，基于 [pi](https://github.com/earendil-works/pi-mono) 的 Agent 运行时构建。

**核心理念：** 大脑（人设、记忆、技能）放云端，操作（文件读写、命令执行）在本地。

## 状态

当前处于骨架阶段——Agent 循环、工具系统、SOUL.md 身份加载已就绪，LLM 接入待完成。

## 快速开始

```bash
npm install
npm run build
node dist/cli.js --soul    # 查看当前身份
node dist/cli.js "hello"   # 单次提示
```

## 架构

```
┌─────────────────────────────────────────┐
│                   云端                    │
│  ┌──────────┐  ┌────────┐  ┌─────────┐ │
│  │ SOUL.md  │  │ Memory │  │ Skills  │ │
│  └──────────┘  └────────┘  └─────────┘ │
│        ▲            ▲           ▲       │
│        │   同步     │           │       │
├────────┼────────────┼───────────┼───────┤
│        ▼            ▼           ▼       │
│             本地 (half-pi)              │
│  ┌────────────────────────────────────┐ │
│  │  pi-ai  │  pi-agent-core  │  tools │ │
│  │  (LLM)  │  (ReAct cycle)  │ (bash, │ │
│  │         │                 │  read, │ │
│  │         │                 │  edit, │ │
│  │         │                 │  skill)│ │
│  └────────────────────────────────────┘ │
└─────────────────────────────────────────┘
```

## 目录

```
half-pi/
├── src/
│   ├── cli.ts                 # CLI 入口
│   ├── index.ts               # 公共 API
│   ├── config.ts              # 配置路径 (~/.half-pi/)
│   └── core/
│       ├── agent-session.ts   # Agent 生命周期封装
│       ├── system-prompt.ts   # System Prompt 构建（SOUL.md 首位注入）
│       ├── soul-loader.ts     # SOUL.md 加载（文件 / 回退默认）
│       ├── skills.ts          # Skill 管理（加载 / 创建 / 删除）
│       ├── tools.ts           # 11 个工具的名称和描述
│       └── tool-impls.ts      # 工具实现（TypeBox schemas + AgentTool）
├── identity/
│   └── SOUL.md                # 身份模板（部署时复制到 ~/.half-pi/SOUL.md）
├── package.json
├── tsconfig.json
├── LICENSE                    # AGPL-3.0
└── README.md
```

## 工具

| 工具 | 功能 |
|------|------|
| `read` | 读文件内容，支持分页 |
| `bash` | 执行终端命令 |
| `edit` | 查找替换编辑文件 |
| `write` | 创建或覆写文件 |
| `grep` | 搜索文件内容（ripgrep） |
| `find` | 按 glob 查找文件（fd） |
| `ls` | 列出目录 |
| `skill_create` | 创建新 Skill |
| `skill_list` | 列出所有 Skill |
| `skill_delete` | 删除 Skill |
| `soul_view` | 查看当前身份 |

## 身份

Agent 的身份由 `~/.half-pi/SOUL.md` 定义——这是 System Prompt 的第一槽位，在每次运行开始时自动加载。文件不存在时使用内置默认。

`identity/SOUL.md` 是模板，部署时复制过去并自定义。

## 依赖

| 包 | 用途 |
|----|------|
| `@earendil-works/pi-ai` | LLM API 抽象（20+ providers） |
| `@earendil-works/pi-agent-core` | Agent 运行时（ReAct 循环、事件系统） |
| `cross-spawn` | 跨平台 shell 执行 |
| `typebox` | 工具参数 schema（TypeScript 原生类型推导） |

编译器：`@typescript/native-preview`（tsgo，TypeScript 7.0 Go 原生编译器，0.47 秒全量构建）。

## 路线图

- [x] Agent 骨架 + ReAct 循环
- [x] 11 个工具（含 skill CRUD）
- [x] SOUL.md 身份加载
- [x] tsgo 编译
- [ ] LLM 接入 + 模型选择
- [ ] 跨 Session 记忆系统
- [ ] 云端同步（SOUL.md + memory + skills）
- [ ] Cron 定时任务
- [ ] 交互式 TUI

## 许可

GNU Affero General Public License v3.0。见 [LICENSE](LICENSE)。

你可以在任何地方使用、修改、分发 half-pi，但如果你在网络上提供服务，修改后的源码必须公开。
