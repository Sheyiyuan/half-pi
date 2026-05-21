# half-pi

> 一半本地，一半云端，陪伴你始终如一

half-pi 是一个陪伴你始终如一的秘书团队。它不是普通的 agent——有人和你说话、陪你写代码、替你跑命令，帮你管理任务，当然，还有更重要的：

**"情绪价值 >> 工程实用价值"**

能够更多地陪你聊天。

> 需要纯工具场景时，请用其他更专业化的 agent。half-pi 的价值不在于工程效率最大化，而在于带给你更多陪伴与成长。

## 架构

```
~/.half-pi/
├── core.SOUL.md           ← 底层承诺（所有 soul 共享）
├── souls/                 ← 灵魂定义目录
│   └── <name>/identity.md ← 每个灵魂的身份卡
├── groups/                ← 群组配置
│   └── <name>.json        ← 群组定义（成员 + 调度规则）
├── style.md               ← 全局风格（可选，按需加载）
├── skills/                ← 技能（所有 soul 可用）
└── memory/                ← 记忆（计划中）
```

### 核心概念

**导演模式** — LLM 不扮演任何灵魂。LLM 是一个对话导演，通过 `speak(soul, text)` 工具让灵魂发言。标签 `[name]` 由程序自动输出，不由模型生成。

**群聊模型** — 所有灵魂在同一上下文中，消息标注来源。用户通过 @ 或自然语言指定谁回应。

**群组** — 用户可以配置多个群组（日常组、工作组等），每组有不同的灵魂成员和调度规则。

## 快速开始

```bash
pnpm install
pnpm run build

# 单次提示
node dist/cli.js "你好"

# 群聊模式
node dist/cli.js chat --group daily
```

## 目录

```
src/
├── cli.ts                 # CLI 入口
├── config.ts              # 配置路径
├── index.ts               # 公共 API
├── core/
         ├── agent-session.ts   # 会话管理 + speak 调度
         ├── soul-loader.ts     # 灵魂加载（core.SOUL.md + identity.md）
         ├── system-prompt.ts   # 提示词构建
         ├── groups.ts          # 群组配置解析
         ├── skills.ts          # 技能管理
         ├── tools.ts           # 工具名称定义
         └── tool-impls.ts      # 工具实现
```

## 自定义

### 灵魂身份

每个 soul 是一个完整的人。`identity.md` 定义它的性格、语气、行为边界。用户可以自由编写或修改。

### 群组配置

```json
{
  "name": "日常",
  "souls": ["arona", "purana"],
  "dispatch": "default",
  "default_soul": "arona",
  "prompt_rules": [
    "你的自定义规则"
  ]
}
```

### 全局风格

`~/.half-pi/style.md` — 不存就不加载。用来定义通用的写作风格、情感基调、反模式、受限视角原则等。适用于所有群组和所有灵魂。

## 工具

| 工具 | 功能 |
|------|------|
| `speak` | 让灵魂发言（导演模式下唯一输出方式） |
| `read` | 读文件 |
| `bash` | 执行命令 |
| `edit` | 查找替换编辑 |
| `write` | 创建或覆写文件 |
| `grep` | 搜索文件内容 |
| `find` | 按 glob 查找文件 |
| `ls` | 列出目录 |
| `skill_create/delete/list` | 技能管理 |
| `soul_view` | 查看当前身份 |

## 鸣谢

本项目基于 [pi](https://github.com/earendil-works/pi) 的 Agent 运行时构建。感谢 [earendil-works](https://github.com/earendil-works) 团队的开源贡献。

## 许可

AGPL-3.0
