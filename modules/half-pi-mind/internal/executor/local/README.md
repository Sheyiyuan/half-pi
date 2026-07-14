# 工具系统

## 当前工具

| 工具 | 平台 | 功能 | 确认 | 分类 |
|------|------|------|------|------|
| `read_file` | 全平台 | 读取文件内容，支持 offset/limit/char_offset | — | 信息获取 |
| `list_files` | 全平台 | 列出文件和目录，支持递归遍历和 glob 过滤 | — | 信息获取 |
| `grep` | 全平台 | 在文件中搜索字面量字符串 | — | 信息获取 |
| `grep_regex` | 全平台 | 在文件中用正则表达式搜索 | — | 信息获取 |
| `write_file` | 全平台 | 创建或覆盖文件 | 默认确认 | 环境操作 |
| `edit_file` | 全平台 | old_string → new_string 精确替换 | 默认确认 | 环境操作 |
| `exec_command` | Unix | 通过 sh 执行命令 | 安全策略 | 环境操作 |
| `exec_cmd` | Windows | 通过 cmd.exe 执行命令 | 安全策略 | 环境操作 |
| `exec_ps` | Windows | 通过 PowerShell 执行命令 | 安全策略 | 环境操作 |
| `check_security` | 全平台 | 预查安全策略结果 | — | 导航辅助 |
| `view_skill` | 全平台 | 按名称加载技能全文 | — | 导航辅助 |

## 工具分类规划

工具按对 LLM 的认知负担分为三类：

### 第一类：信息获取
只读操作，不改变状态。LLM 犯错成本低——最多读错文件或列出错目录，不会有副作用。

- ✅ `read_file`、`list_files`、`grep`、`grep_regex`
- 📋 规划中：`stat`（文件属性）

### 第二类：环境操作
会改变系统状态，但影响范围有限。需要安全策略参与。

- ✅ `exec_command` / `exec_cmd` / `exec_ps`、`write_file`、`edit_file`
- 📋 规划中：`mkdir`、`delete_file`

### 第三类：导航辅助
信息获取的子集，但独立一类的理由是——它不获取用户数据，而是帮 LLM 理解自身的行为边界。LLM 用它来"提问而非执行"。

- ✅ `check_security`、`view_skill`
- 📋 规划中：`list_tools`（列出可用工具）、`help`（工具使用说明）

---

## 设计思路

### 核心原则：让工具"难以用错"

不是给 LLM 最多选择，而是让它用最少的工具、最准的参数完成最多的任务。每条工具的设计都问三个问题：

1. **LLM 能否清晰理解它的边界？** — 描述必须精确，让 LLM 不会在 A 和 B 之间犹豫。
2. **参数是否有安全的默认行为？** — timeout 默认 30s、path 默认当前目录。
3. **犯错的代价多大？** — 只读工具犯错代价为零，写操作需要 Check hook 或 DefaultConfirm。

### 为什么不做"万能工具"

一个 `run_code` 工具能写文件 + 执行 + 安装依赖，看似一个工具搞定一切。但 LLM 面对一个超强工具时，倾向于"什么任务都塞给它"，参数组合爆炸，错误率飙升。

Half-Pi 宁可要 10 个 LLM 不会用错的工具，也不要 1 个能做一切但 LLM 经常用错的。

---

## 不可能三角：工具数量 · 任务支持 · 低错误率

```
         工具数量
           /\
          /  \
         /    \
        /  🎯  \
       /________\
 任务支持        低错误率
```

三者天然冲突：

| 选择 | 代价 |
|------|------|
| 工具多 → 任务覆盖广 | LLM 选错工具的概率上升 |
| 工具少 → 准确率高 | 可完成的任务类型受限 |
| 参数灵活 → 表达能力强 | LLM 容易生成非法参数组合 |

### Half-Pi 的平衡策略

**1. 用分类降低选择空间**

LLM 不需要从 20 个工具里选，只需要先判断场景（「是读还是写？」「是文件还是命令？」），然后在 2~3 个工具里精确匹配。当前工具按功能聚类，同类工具互换成本低、选错代价小。

**2. 参数类型越严格越好**

- `path` 是 `string`，不是自由文本——LLM 有明确的模式匹配锚点
- `timeout` 是 `number`，不是 `string`——避免了「30 秒」和「30s」的歧义
- `command` 虽然也是 `string`，但有 `check_security` 工具可预检，降低误用代价

**3. 工具能力边界清晰，不重叠**

`exec_command` 执行命令，`read_file` 读文件，`list_dir` 列目录。一个任务只有一种正确答案：读文件用 `read_file`，不会有人用 `exec_command` 跑 `cat`。

**4. 安全层兜底**

即使 LLM 选错工具、选对工具但填错参数，安全策略在 Check hook 层拦截危险操作。这意味着工具数量的增加不会线性拉高风险。

**5. 按需添加，不提前预装**

当前有 11 个工具（全平台 8 个 + 平台特定 3 个），覆盖"搜索定位 → 浏览文件 → 编辑修改 → 写入文件 → 执行命令 → 技能查阅 → 检查安全性"的完整工作流。

- 出现「LLM 因为没有某工具而反复失败」的场景 → 加工具
- 出现「LLM 在 A 和 B 之间反复选错」的场景 → 合并或细化描述
- 不加「将来可能用到」的工具

---

## 添加新工具

在 `internal/executor/local/` 下新建 `tool_<name>.go`：

### 模板

```go
// <一句话说明工具用途>
package local

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
)

func init() {
    executor.Register(executor.Tool{
        Name:        "<name>",
        Description: "<LLM 理解工具用途的说明>",
        Parameters: &executor.ObjectSchema{
            Properties: []executor.PropertySchema{
                {Name: "<参数名>", Type: "string", Description: "<参数说明>"},
            },
            Required: []string{"<必填参数名>"},
        },
        // Check: ...   // 有副作用时实现安全检查
        // DefaultConfirm: true,  // 需每次确认时启用
        Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
            var p struct {
                // 参数字段
            }
            if err := json.Unmarshal(args, &p); err != nil {
                return &executor.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}
            }
            // 参数校验
            // 执行逻辑
            return &executor.ToolResult{Success: true, Output: "结果"}
        },
    })
}
```

### 注册前检查清单

- [ ] 工具名不与其他工具重叠（不同名但同功能也是重叠）
- [ ] Description 足以让 LLM 区分它和同类工具
- [ ] 参数类型精确（不要用 string 承载 number / bool）
- [ ] 有副作用的操作实现 `Check` 或设置 `DefaultConfirm`
- [ ] 跨平台时用 `_unix.go` / `_windows.go` + build tag 区分
- [ ] 到此 README 的工具表中注册
