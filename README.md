<p align="center">
  <img src="docs/images/logo.svg" width="120" alt="half-pi" />
</p>

<h1 align="center">Half-Pi · 半派</h1>

<p align="center">
  <em>一半本地，一半云端——陪伴你始终如一</em>
</p>

---

Half-Pi 是一套 Face-Mind-Hand 三端分离的远程设备操控系统。

一个 AI 意识（Mind）作为唯一的记忆与决策核心，通过多台远程设备（Hand）精准执行，用户通过统一的交互界面（Face）与之对话。跨设备的上下文与记忆始终保持全局一致。

不是远程控制台加 AI 插件——Mind 是整个系统的中心。

## 架构

```
Face  ←→  Mind  ←→  Hand
```

| 角色 | 职责 | 实现 |
|------|------|------|
| **Face** | 用户交互层，不持有任何状态 | Bubble Tea TUI / WebUI / IM Bot |
| **Mind** | 唯一的智能节点，维护全局记忆与决策 | Go 服务端 |
| **Hand** | 纯执行者，常驻被控设备 | Go 编译的单一二进制守护进程 |

详细设计：[agent 设计文档](https://github.com/Sheyiyuan/half-pi)（待补充）

## 核心设计

- **关注点分离**：Face 只做 I/O，Mind 只做决策，Hand 只做执行
- **联邦黑白名单**：服务端 + 客户端双层安全规则
- **四种风险模式**：Strict / Normal / Trust / YOLO
- **全链路审计**：每步操作可追溯
- **跨平台会话**：所有 Face 共享同一上下文

## 状态

设计定稿，准备进入 Phase 1 开发。

## 许可

AGPL-3.0
