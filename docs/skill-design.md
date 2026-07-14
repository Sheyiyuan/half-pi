# Skill 系统设计

> 状态：草稿

## 技能文件

技能是带 frontmatter 的 markdown 文件，放 `~/.half-pi/skills/`。

### 文件格式

```markdown
---
name: go-patterns
description: Go 项目结构和代码规范
tags: [go, coding]
version: 1.0.0
author: syy
---

# Go 项目规范

## 结构

- 内部包放 `internal/`
- 每个包一个目录，包名小写
- ...

## 测试

- 使用标准库 `testing`
- 测试文件放同包内 `*_test.go`
```

### frontmatter 字段

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | ✅ | 技能唯一标识 |
| `description` | ✅ | 一句话说明，LLM 据此判断是否需要加载 |
| `tags` | ❌ | 分类标签 |
| `version` | ❌ | 版本号 |
| `author` | ❌ | 创建者 |

## 加载流程

```
启动 / 会话创建
  → 根据当前作用域（工作区/会话）确定启用的技能列表
  → 扫描技能目录，匹配启用的技能
  → 提取匹配技能的 frontmatter 编入 system prompt
  → LLM 看到技能索引

对话中
  → LLM 调 view_skill("go-patterns")
  → 读取完整技能文件
  → 内容作为 system 消息注入对话历史
  → LLM 下一轮看到技能正文
```

### System prompt 中的索引格式

```
可用技能：
  go-patterns    — Go 项目结构和代码规范
  deploy-flow    — 标准部署流程

查看详情：view_skill("<名称>")
```

## 工具接口

### view_skill

按名称加载技能全文，注入对话。

```go
Name: "view_skill"
Parameters: { name: string }
```

### create_skill

创建新技能文件。

```go
Name: "create_skill"
Parameters: { name, description, content: string }
```

### reload_skills

重新扫描技能目录，刷新 frontmatter 缓存。

```go
Name: "reload_skills"
Parameters: {}  // 无参数
```

## 文件存储

- 存放目录：`~/.half-pi/skills/`
- 命名规则：`<name>.skill.md`
- 来源：用户手动创建、AI 通过 `create_skill` 创建、官方技能包（规划中）

## 实现阶段

### Phase 1（MVP）

- 扫描 `~/.half-pi/skills/` 下所有 `.skill.md` 文件
- 启动时将全部技能 frontmatter 编入 system prompt
- `view_skill` 工具加载全文
- `create_skill` 工具创建新技能
- 无作用域过滤（全部可用）

### Phase 2（与工作区集成）

- 技能按工作区（SessionGroup）过滤
- session 级别可覆盖/禁用
- `reload_skills` 工具

### Phase 3（分发）

- 官方技能仓库
- 技能版本管理
- 技能依赖
