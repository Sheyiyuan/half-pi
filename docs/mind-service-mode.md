# Mind 服务模式设计

## 现状问题

当前 Mind 默认启动 REPL（交互式终端），WS Hub 服务端作为副产物运行。
这隐含了一个假设：每次启动 Mind 的人都会坐在终端前与它对话。

实际情况是：

- Mind 是一台**服务**，应该默认在后台安静运行
- 用户通过 Face（TUI / WebUI / IM Bot）与 Mind 交互，而非直接操作终端
- REPL 是开发调试工具，不应是默认入口

## 设计

### 命令行接口

```
mind              # 默认：后台服务模式（无 REPL，仅 WS Hub）
mind --repl       # 可选：交互式 REPL 模式（WS Hub + REPL）
mind --version    # 打印版本号
```

`--daemon` 不出现。后台模式就是默认行为，不是「特殊模式」。

### 默认模式（无 `--repl`）

**启动流程：**

```
main()
├── 加载配置（~/.half-pi/config.toml + 环境变量覆盖）
├── setup.Init() 确保 ~/.half-pi/ 目录和默认配置文件存在
├── 初始化 Store（SQLite）
├── 创建 Hub（WebSocket 服务端）
├── 鉴权（hand_tokens 表）
├── 启动 HTTP/WS 服务器（:15707）
├── 写入 PID 文件（~/.half-pi/mind.pid）
├── 注册信号处理（SIGTERM/SIGINT → 优雅关闭）
└── 等待信号 / 连接
```

**行为特征：**

- 不监听 stdin，不启动读行循环
- 所有输出走 log 输出（`~/.half-pi/logs/mind.log`），不写 stdout（除非 `--verbose`）
- SIGTERM/SIGINT 触发优雅关闭：等待活跃连接完成 → 清理 PID 文件 → 退出
- 不依赖任何交互式终端——可在 systemd、Docker、launchd 下运行

**不做的事（当不在 REPL 模式时）：**

- 不启动 Agent Core（无 LLM 循环）。Mind 作为 Hub 只是消息路由层
- LLM 循环由 Face 连接后触发（类似 REPL 中用户输入 → LLM 响应，但远程 Face 发消息 → LLM 响应）
- 不打印启动 banner、不输出事件到终端

### REPL 模式（`--repl`）

当前行为基本不变：

```
mind --repl
```

- WS Hub + REPL 同时运行
- 所有事件/输出打到 stderr/stdout
- 可输入 `/mode`、`/hand`、`/peers` 等命令
- 可进入 Chat 循环直接和 LLM 对话

### Hand 的对齐

Hand 的默认行为不变——它本身就是后台模式：
```
hand                              # 后台连接，无交互
hand --verbose                    # 打开 stderr 日志输出
```

后续加上自动重连（指数退避，最大 30 秒间隔）。

## 系统集成

### systemd 示例

`~/.half-pi/examples/half-pi-mind.service`：

```ini
[Unit]
Description=Half-Pi Mind — 跨设备 AI 助理核心
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/half-pi-mind
Restart=on-failure
RestartSec=5
User=hanpai
PIDFile=/home/hanpai/.half-pi/mind.pid

[Install]
WantedBy=multi-user.target
```

### 日志

默认模式：日志写入 `~/.half-pi/logs/mind.log`，按大小轮转（暂定 10MB × 3 轮）。

REPL 模式：日志同时打到 stderr。

## 文件变更

| 文件 | 变更 |
|------|------|
| `modules/half-pi-mind/cmd/half-pi-mind/main.go` | 加 `--repl` flag；默认只起 Hub 不启动 REPL；REPL 模式才启动读行循环和 Chat；加入 PID 文件管理 |
| `modules/half-pi-mind/cmd/half-pi-mind/main.go` | 信号处理：SIGTERM 优雅关闭 |
| `~/.half-pi/examples/half-pi-mind.service` | 新增 systemd 示例 |

## 为什么不做 `--daemon`

「守护进程」这个词隐含了「前台 → 后台」的转换——fork、setsid、双进程。这在容器化和 systemd 时代已经不必要了。

- systemd 只需要 `Type=simple`
- Docker 只需要 `CMD` 不加任何 daemon 包装
- 后台化、重启策略、日志管理都是宿主系统（systemd/Docker/launchd）的职责

所以：**Mind 默认是无交互的长期进程，但不是传统意义上的 daemon。** 不加 `--daemon` 参数，因为默认就是它。
