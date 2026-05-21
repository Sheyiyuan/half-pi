# half-pi

> half cloud, half local — a self-improving coding agent

half-pi 是一个基于 [pi](https://github.com/earendil-works/pi-mono) 的自我迭代 Agent 框架。

**核心理念：** 大脑（人设+记忆）放云端，操作在本地。

## 架构

```
┌─────────────────────────────────────┐
│                 云端                  │
│  ┌──────────┐  ┌────────┐  ┌──────┐ │
│  │ SOUL.md  │  │ Memory │  │Skills│  │
│  └──────────┘  └────────┘  └──────┘  │
│        ▲            ▲          ▲      │
│        │   同步     │          │      │
├────────┼────────────┼──────────┼──────┤
│        ▼            ▼          ▼      │
│  ┌──────────────────────────────────┐ │
│  │           half-pi Agent          │ │
│  │   pi-ai + pi-agent-core + tools  │ │
│  └──────────────────────────────────┘ │
│                 本地                   │
└─────────────────────────────────────┘
```

## 目录结构

```
half-pi/
├── src/
│   ├── cli.ts              # CLI 入口
│   ├── index.ts            # 公共 API
│   ├── config.ts           # 配置路径 (~/.half-pi/)
│   └── core/
│       ├── agent-session.ts   # Agent 封装
│       ├── system-prompt.ts   # System Prompt（SOUL.md 第一槽位）
│       ├── soul-loader.ts     # SOUL.md 加载
│       ├── skills.ts          # Skill CRUD
│       ├── tools.ts           # 工具名和描述
│       └── tool-impls.ts      # 11 个工具实现
├── package.json
├── tsconfig.json
├── SOUL.md                 # 身份模板
└── README.md
```

## 依赖

| 包 | 用途 |
|----|------|
| `@earendil-works/pi-ai` | LLM 抽象层（20+ providers） |
| `@earendil-works/pi-agent-core` | Agent 运行时（ReAct 循环 + Agent 类） |

## 命令

```bash
npm run build       # 编译
npm run check       # 类型检查
npm run dev         # 开发模式直接运行

# CLI
half-pi "prompt"              # 单次提示
half-pi --soul                # 查看 SOUL.md
half-pi --system-prompt <txt> # 临时覆盖身份
```

## 配置

- `~/.half-pi/SOUL.md` — Agent 身份定义
- `~/.half-pi/skills/` — 可复用的 Skill 文档
- `~/.half-pi/memory/` — 跨 Session 记忆（计划中）

## 许可

MIT
