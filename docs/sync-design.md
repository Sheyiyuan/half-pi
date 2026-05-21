# 同步模块设计

> 状态：设计中，未实现
> 更新：2026-05-21

## 设计原则

1. **统一软件，模块化。** 不区分 agent 和 server——所有 half-pi 实例跑同一份代码。中心节点只是「开启了 sync 模块的实例」。服务模块（sync、web、onebot）是可选插件。
2. **逻辑中心化，物理 p2p。** 只有一个 merge hub（真理源），但它可以是任意设备。其他节点离线时照常运行，在线时自动同步。
3. **安全分层。** 开启服务模块的节点自动进入受限模式（chroot/Docker 沙箱），防止远程操作影响宿主系统。
4. **冲突个人场景优先。** 不追求 Google Docs 级实时协作，last-write-wins + 保留副本即可。

---

## 1. 拓扑模型

### 中心-叶子拓扑

```
        [Phone:leaf]  ←→  [NAS:center]  ←→  [Workstation:leaf]
                                ↑
                          [Laptop:leaf]

center:  唯一 merge hub，运行 sync 模块暴露端口
leaf:    连接 center 的普通节点
```

### 关键约束

| 约束 | 说明 |
|------|------|
| 中心唯一 | 同一时刻只有 1 个节点充当 center。通过配置文件手动指定，不做自动选主 |
| 叶子自治 | 叶子离线时完全正常工作。变更攒在本地 git commit，重连后 push |
| 叶子间不直接通信 | 所有同步流量经过 center——冲突处理只需在一个地方做 |
| center 离线 - leaf 正常 | leaf 照常运行，重连后批量 push |

### 选主逻辑（手动配置）

不搞 Raft/Paxos。在 `~/.half-pi/config.yaml` 中：

```yaml
# 中心节点配置
sync:
  mode: center                    # center | leaf | off
  port: 2222                      # Git SSH 端口（或 HTTPS 443）
  bind: "0.0.0.0"
  tls:
    enabled: true                 # 强制 HTTPS
    cert: "~/.half-pi/certs/fullchain.pem"
    key: "~/.half-pi/certs/privkey.pem"

# 叶子节点配置
sync:
  mode: leaf
  remote: "git@nas.local:half-pi-memory"  # 或 https://nas.local/half-pi-memory
  auto_sync_interval: 300                  # 自动同步间隔（秒），0 为手动
```

`mode: off` 的节点完全不参与同步，只使用本地记忆。

---

## 2. 数据流

### 同步内容

```
~/.half-pi/memory/cloud/    ← 全量 git 同步（所有设备共享）
~/.half-pi/skills/          ← 全量 git 同步（skill 定义）
~/.half-pi/SOUL.md          ← 全量 git 同步（Agent 身份）

不同步的：
~/.half-pi/memory/local/    ← 设备绑定，.gitignore
~/.half-pi/config.yaml      ← 设备绑定（含 API key、端口等敏感信息）
~/.half-pi/sessions/        ← session 日志，设备绑定
~/.half-pi/certs/           ← TLS 证书，设备绑定
```

### 同步方向

```
leaf → center:  push（本地变更上传）
center → leaf:  pull（合并后的最新状态拉取）

center 负责：
  1. 接收所有 leaf 的 push
  2. 执行 L1/L2 自动合并
  3. L3 冲突保留副本（*.conflict.md），标记待处理
  4. 定期用 agent 整理（合并、归档、清理冲突副本）
  5. 作为 Web 模块的数据源
```

### 叶子节点同步流程

```
定时器触发 / 手动触发 (half-pi sync)
  │
  ├── 1. git fetch origin
  │
  ├── 2. 检查本地未推送 commit
  │      ├── 有未推送 → git push origin
  │      └── 无 → 跳过
  │
  ├── 3. git pull --rebase origin main
  │      ├── rebase 成功 → done
  │      ├── rebase 冲突 → 中止，进入冲突处理
  │      │   ├── 保留本地版本为 *.local-backup.md
  │      │   ├── 拉取远程版本
  │      │   └── 标记冲突，通知用户（Web / CLI）
  │      └── 网络不可达 → 跳过，下次重试
  │
  └── 4. 触发记忆重载（更新 Agent 上下文）
```

---

## 3. 传输层

### 首选：Git

个人使用场景（< 5 设备，< 200 条记忆），Git 是最务实的传输层：

- 自带冲突检测（git merge / rebase）
- 自带版本历史（可以回溯任意时间点的记忆状态）
- 零开发成本（不需要自造协议）
- HTTPS 天然 TSL（配合 nginx/caddy）

### Git 裸仓库设置

```
# 在 center 节点上
mkdir -p ~/.half-pi/repo/
git init --bare ~/.half-pi/repo/memory.git

# 叶子节点 clone
git clone ssh://nas.local:2222/~/half-pi/repo/memory.git ~/.half-pi/memory/
```

### 备选：自造 HTTP API（远期）

如果需要更细粒度的同步控制（增量同步、部分更新、推送通知），可以升级到 HTTP API：

```
POST   /sync/push          ← leaf 上传变更（diff 格式）
GET    /sync/pull?since=X  ← leaf 拉取增量
POST   /sync/conflict/:id  ← Web 端手动解决冲突
GET    /sync/status        ← 查看同步状态
```

但 Phase 1 用 Git 完全够。接口预留，不提前实现。

---

## 4. 冲突处理三层策略

| 层级 | 触发条件 | 处理方式 | 自动/人工 |
|------|----------|----------|-----------|
| **L1: 自动合并** | 不同段落被修改（Git 自动 merged） | 直接 accept merge result | 全自动 |
| **L2: 可判定** | 同一段落，但一方仅追加（另一方没动该处） | 保留追加版本 | 全自动 |
| **L3: 真正冲突** | 同一段落被两边都修改了 | 保留两个副本，Web 上标记待处理 | 人工在 Web 上解决 |

### L3 冲突文件命名

```
cloud/pref-editor.md                ← center merge 后的版本（优先保留）
cloud/pref-editor.conflict.md       ← 冲突的另一个版本（带来源时间戳）
cloud/pref-editor.conflict.leaf-laptop-2026-05-21T15:30:00.md  ← 原始冲突
```

Web 端展示 diff 视图，用户选择保留哪一方（或手动编辑合并）。

### 自动 merge 操作由中心 agent 执行

中心 node 的 half-pi agent 通过 cron 定期执行：

```
1. git pull（收集所有 leaf 的推送）
2. git merge（L1 自动解决）
3. 扫描 diff：检测 L2 场景（同段落仅追加）
4. 标记 L3 冲突（*.conflict.md）
5. git commit + push（广播合并结果）
6. 更新 Web 端待处理队列
```

---

## 5. 中心 Agent 定期整理

中心节点上运行 cron job，每周执行一次记忆整理：

```
weekly-tidy:
  1. 扫描 ~/.half-pi/memory/cloud/*.conflict.md
     ├── 已存在 > 7 天且未解决 → 推送到 Web 端提醒用户
     └── 用户已解决（conflict 文件不存在了）→ 无操作

  2. 检测相似记忆（标签重叠 ≥ 2 + 同类型）
     → Agent 生成合并建议，推送到 Web 端待确认

  3. 标记归档候选（last_called > 90 天 且 priority ≠ critical）
     → Agent 生成归档列表，推送到 Web 端待确认

  4. 清理孤立记忆（weight < 0.15 且 created > 180 天）
     → Agent 建议删除

  5. 执行已确认的操作（用户在 Web 端点了确认的项）
     → git commit + push 广播
```

### 关键设计：中心 agent 不做语义决策

L3 冲突的内容选择、合并建议、归档确认——这些决策**必须经过人在 Web 端确认**。中心 agent 只做：

- 检测（发现冲突、发现相似、发现可归档）
- 建议（提供 diff 视图、合并方案）
- 执行已确认的操作
- 清理已解决的残留文件

它不能替用户决定「哪份记忆版本是对的」。

---

## 6. 安全设计：沙箱隔离

开启 sync 模块的节点进入受限模式。

### 两层隔离

| 级别 | 方案 | 适用场景 | 隔离程度 |
|------|------|---------|---------|
| **Level 1** | 路径白名单 | 个人 NAS、家庭服务器 | bash/write/edit 限制在 ~/.half-pi/ 和指定 workspace |
| **Level 2** | Docker 容器 | 公网 VPS、云服务器 | 进程完全隔离，挂载特定 volume |

### Level 1：路径白名单

```yaml
# ~/.half-pi/config.yaml（center 节点）
sandbox:
  level: path_whitelist
  allowed_paths:
    - ~/.half-pi/           # 配置和记忆（默认）
    - /data/workspace/      # 额外允许的 workspace
  allow_network: true       # 允许 bash 工具使用网络
  allow_package_manager: false  # 禁止 apt/pip/npm install
```

工具层实现：`bash`、`write`、`edit` 在执行前对目标路径做 `isAllowedPath()` 检查。不在白名单的路径返回拒绝。

### Level 2：Docker 容器

```yaml
sandbox:
  level: docker
  image: half-pi-sandbox:latest  # 预装了 ripgrep、fd、git
  volumes:
    - ~/.half-pi:/root/.half-pi:rw
    - /data/workspace:/workspace:rw
  network: bridge              # 隔离网络
  cap_drop:
    - ALL                      # 丢弃所有 Linux capabilities
  readonly_rootfs: true        # 只读根文件系统
```

容器内 half-pi 作为非 root 用户运行，移除所有特权 capability。

---

## 7. Web 模块：移动端交互入口

Web 不是「管理系统」，是**离开电脑和命令行后与 half-pi 交互的入口**。

### 定位

| 不是 | 是 |
|------|----|
| CRUD dashboard | 移动端聊天界面 |
| 表格式管理后台 | 记忆浏览器 |
| 配置编辑器 | 流式响应展示 |

### 核心能力

```
Web 模块（运行在 center 节点上）
  ├── /chat                  ← 主界面：prompt 输入 + 流式响应 + 工具调用可视化
  ├── /memory                ← 记忆浏览（时间线视图，不是表格）
  │   ├── /memory/search     ← 搜索记忆
  │   └── /memory/conflicts  ← 待解决的冲突（diff 视图 + 选择）
  ├── /soul                  ← 查看当前 SOUL.md
  ├── /sessions              ← 查看历史 session（只读）
  └── /health                ← 节点状态
```

### 技术栈

| 层 | 选型 |
|----|------|
| 后端 | half-pi 内置 HTTP server（Node.js `http` 模块），不做 express/koa |
| 前端 | 纯 HTML + 少量 JS（SSE 流式响应），不拉框架 |
| 认证 | API token（在 config.yaml 中配置），手机浏览器存 token |
| 响应式 | 移动端优先，PC 也能用但不优化 |

### 流式响应

```
GET /api/chat?prompt=hello
  → text/event-stream (SSE)
  → data: {"type":"text","content":"你好"}
  → data: {"type":"tool_call","name":"read","params":{"path":"..."}}
  → data: {"type":"tool_result","content":"..."}
  → data: {"type":"done"}
```

### 安全

Web 模块默认仅监听 localhost（不对外）。用户通过 nginx/caddy 反向代理 + HTTPS 暴露到公网。

---

## 8. OneBot 模块：IM 桥接

OneBot 是开放的 QQ 机器人协议标准（v11/v12），让 half-pi 接入 QQ/微信。

### 架构

```
[QQ/微信] ←→ [OneBot 客户端（如 Lagrange/NapCat）]
                    ↑ WebSocket
              [half-pi OneBot 模块]
                    ↓
              [half-pi Agent 核心]
```

half-pi 不实现 QQ 协议——它只实现 OneBot 消费者端（WebSocket 连接）。QQ 协议由成熟的 OneBot 客户端处理。

### 消息流

```
1. 用户在 QQ 中 @half-pi 发送消息
2. OneBot 客户端 → WebSocket → half-pi OneBot 模块
3. OneBot 模块 → AgentSession.prompt()
4. 流式响应 → 分段发送到 QQ（模拟逐条消息）
5. 工具调用可视化 → 发送为 QQ 消息（代码块格式）
```

### 配置

```yaml
onebot:
  enabled: true
  ws_url: "ws://localhost:3001"   # OneBot 客户端地址
  access_token: "your-token"
  allowlist:                      # 允许使用的 QQ 号
    - "123456789"
```

---

## 9. 配置全景

### 节点类型对照

| 配置项 | leaf 节点 | center 节点 | off 节点 |
|--------|----------|------------|----------|
| `sync.mode` | leaf | center | off |
| sandbox | 不需要 | path_whitelist / docker | 不需要 |
| web 模块 | 不启用 | 可选 | 不启用 |
| onebot 模块 | 不启用 | 可选 | 不启用 |
| 记忆同步 | 双向（push+pull） | 接收 + merge | 无 |

### 完整 config.yaml 结构

```yaml
# ~/.half-pi/config.yaml
half_pi:
  name: "my-nas"            # 节点名称（用于日志和 Web 显示）
  model: "deepseek-v3"      # 默认模型

sync:
  mode: center              # center | leaf | off
  port: 2222                # SSH port（center 模式）
  remote: ""                # leaf 模式下的 remote URL
  auto_sync_interval: 300   # 自动同步间隔（秒）

sandbox:
  level: path_whitelist     # path_whitelist | docker | none
  allowed_paths:            # level=path_whitelist 时生效
    - ~/.half-pi/
    - /data/workspace/

web:
  enabled: true
  port: 3000
  bind: "127.0.0.1"        # 默认 localhost，由反向代理暴露
  token: "your-api-token"

onebot:
  enabled: false
  ws_url: "ws://localhost:3001"
  access_token: ""
  allowlist: []
```

---

## 10. 实现阶段

| 阶段 | 内容 | 输出 |
|------|------|------|
| **Phase 0** | 前置：LLM 接入打通（half-pi 能跑 agent 循环） | cli.ts 完成 |
| **Phase 1** | Git 同步核心：clone/init、push/pull、rebase 冲突标记 | `sync-git.ts` |
| **Phase 2** | 冲突分层：L1 自动 merge、L2 追加检测、L3 副本保留 | `sync-conflict.ts` |
| **Phase 3** | 路径白名单沙箱（`isAllowedPath()` + 工具层集成） | `sandbox-whitelist.ts` |
| **Phase 4** | Web 模块：chat SSE + memory 浏览 + conflict 解决页 | `web-server.ts` + 前端 |
| **Phase 5** | Docker 沙箱模式 + OneBot 模块 | `sandbox-docker.ts` + `onebot-ws.ts` |
| **Phase 6** | 中心 agent cron 自动整理（L1/L2 清理 + L3 提醒 + 归档） | `cron-tidy.ts` |

Phase 1-2 是最小同步可用版本——离线攒 commit → 在线 push/pull → 冲突保留副本。
