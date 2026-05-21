# half-pi

> half cloud, half local — a self-improving agent forked from pi

half-pi 是一个基于 [pi](https://github.com/earendil-works/pi-mono) 的自我迭代 Agent 框架。

**核心理念：** 大脑（人设+记忆）放云端，操作在本地。

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
│  ┌──────────┐  ┌────────┐  ┌─────────┐ │
│  │ SOUL.md  │  │ Memory │  │ Skills  │ │
│  └──────────┘  └────────┘  └─────────┘ │
│             本地 (half-pi)              │
│  ┌────────────────────────────────────┐ │
│  │         Agent Runtime (pi)         │ │
│  │   agent-loop / Agent / tools       │ │
│  └────────────────────────────────────┘ │
└─────────────────────────────────────────┘
```

## 目录结构

```
half-pi/
├── packages/
│   ├── ai/          # LLM 抽象层 (from pi)
│   ├── agent/       # Agent 运行时 (from pi)
│   ├── tui/         # 终端 UI 库 (from pi)
│   └── half-pi/     # half-pi 自己的层
│       └── src/
│           ├── cli.ts              # 入口
│           ├── index.ts            # 公共 API
│           └── core/
│               ├── agent-session.ts   # Session 封装
│               ├── system-prompt.ts   # System Prompt（含 SOUL.md）
│               ├── soul-loader.ts     # SOUL.md 加载
│               ├── skills.ts          # Skill 管理（CRUD）
│               ├── tools.ts           # 工具名称和 schema
│               └── tool-impls.ts      # 工具实现
├── package.json
├── tsconfig.json
├── tsconfig.base.json
└── README.md
```

## 与 pi 的关系

half-pi 复用 pi 的三层：

| pi 层 | 作用 | half-pi 是否改动 |
|-------|------|----------------|
| `packages/ai` | LLM API 抽象（20+ providers） | 不修改，直接复制 |
| `packages/agent` | Agent 运行时（ReAct 循环 + Agent 类） | 不修改，直接复制 |
| `packages/tui` | 终端 UI 库 | 不修改，直接复制 |
| `packages/coding-agent` | pi 的应用层 | **不复制**，half-pi 有自己的 |

half-pi 的新增在 `packages/half-pi/`，对应 pi 的 `packages/coding-agent/`。

## 配置

- `~/.half-pi/SOUL.md` — Agent 身份定义
- `~/.half-pi/skills/` — 可复用的 Skill 文档
- `~/.half-pi/memory/` — 跨 Session 持久化记忆（计划中）

## 许可

MIT
