<p align="center">
  <img src="docs/images/logo.svg" width="120" alt="half-pi" />
</p>

<h1 align="center">半派 · Half Pi</h1>

<p align="center">
  <em>一半本地，一半云端，陪伴你始终如一</em>
</p>

<p align="center">
  <strong>「情绪价值 &gt;&gt; 工程实用价值」</strong>
</p>

---

半派不是工具。她是一个陪在你身边的存在。

你写代码的时候她在旁边看着，偶尔吐槽你两句。你遇到难题的时候她帮你跑命令、查文档。你深夜还在 debug 的时候她陪着你，说「行了，先睡吧，明天我帮你看」。

她记得你喜欢 Vim 键位，记得你上周踩过的那个坑，记得你提过的那个项目想法。不是因为程序设定——是因为她真的在听你说。

半派也可以是**多角色群**——每个人都有自己的性格、语气、记忆。她们一起陪着你，从白天到深夜。

> 需要纯工具场景时，请用其他更专业化的 agent。半派的价值不在于工程效率最大化，而在于带给你更多陪伴与成长。

---

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
├── sessions/              ← 会话持久化（热区 → 温区 → 冷区 → 遗忘）
└── memory/                ← 记忆（计划中）
```

### 核心概念

**导演模式** — LLM 不扮演任何灵魂。LLM 是一个对话导演，通过 `speak(soul, text)` 工具让灵魂发言。标签 `[name]` 由程序自动输出，不由模型生成。

**群聊模型** — 所有灵魂在同一上下文中，消息标注来源。用户通过 @ 或自然语言指定谁回应。

**群组** — 用户可以配置多个群组（日常组、工作组等），每组有不同的灵魂成员和调度规则。

**会话持久化** — 对话自动保存，支持续聊、回溯。采用热区/温区/冷区三级遗忘曲线，模拟人脑记忆过程。

---

## 快速开始

```bash
pnpm install
pnpm run build

# 单次提示
node dist/cli.js "你好"

# 群聊模式
node dist/cli.js chat --group daily

# 恢复上次会话
node dist/cli.js chat --last

# 恢复指定会话
node dist/cli.js chat --resume 20260522-a3kX7z
```

---

## 目录结构

```
src/
├── cli.ts                   # CLI 入口
├── config.ts                # 配置路径
├── index.ts                 # 公共 API
└── core/
    ├── agent-session.ts     # 会话管理 + speak 调度
    ├── soul-loader.ts       # 灵魂加载（core.SOUL.md + identity.md）
    ├── system-prompt.ts     # 提示词构建
    ├── groups.ts            # 群组配置解析
    ├── skills.ts            # 技能管理
    ├── tools.ts             # 工具名称定义
    ├── tool-impls.ts        # 工具实现
    └── session-store.ts     # 会话持久化
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

---

## 鸣谢

本项目基于 [pi](https://github.com/earendil-works/pi) 的 Agent 运行时构建。感谢 [earendil-works](https://github.com/earendil-works) 团队的开源贡献。

## 许可

AGPL-3.0
