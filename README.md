<p align="center">
  <img src="docs/images/logo.svg" width="120" alt="half-pi" />
</p>

<h1 align="center">Half-Pi · 半派</h1>

<p align="center">
  <em>一半本地，一半云端——陪伴你始终如一</em>
</p>

---

Half-Pi 是一套 Face-Mind-Hand 三端分离的远程设备操控系统。

一个 AI 意识（Mind）作为唯一的记忆与决策核心，通过多台远程设备（Hand）
精准执行，用户通过统一的交互界面（Face）与之对话。

## 架构

```
Face  ←→  Mind  ←→  Hand
```

| 角色 | 职责 | 实现状态 |
|------|------|----------|
| **Mind** | 唯一的智能节点，工具调用 + 安全审批 + 事件总线 | 🟢 Go REPL 可用 |
| **Face** | 用户交互层，不持有任何状态 | ⚪ 待开发 |
| **Hand** | 纯执行者，常驻被控设备 | ⚪ 待开发 |

## 当前进展（Phase 1）

- ✅ 工具系统：init() 自注册，独立文件，通用 confirm 参数
- ✅ 安全策略：strict / normal / trust / yolo 四模式，y/n/Y/N 审批
- ✅ 事件总线：EventBus + ConsoleWriter + FileWriter
- ✅ 环境初始化：~/.half-pi/ 目录，config.toml，编译时 OS 区分
- ✅ 配置加载：TOML 解析，模型/提供商定义，环境变量密钥覆盖
- ✅ 执行工具：exec_command、read_file、list_dir、check_security
- ✅ 工具模板：example.md 规范，添加新工具只需一个文件 + init()

详细开发进度见 [AGENTS.md](AGENTS.md)。

## 快速开始

```bash
# 首次使用：编辑配置填入 API Key
vim ~/.half-pi/config.toml

# 启动 REPL
cd modules/half-pi-mind && go run ./cmd/half-pi-mind/
```

## 许可

AGPL-3.0
