package executor

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// ToolRegistrySnapshot 是工具定义在一个 revision 上的不可变规范视图。
type ToolRegistrySnapshot struct {
	Revision uint64
	Tools    []Tool
	Digest   string
}

// Catalog 是一组工具定义的独立注册表。
// 每个 Catalog 拥有自己的工具集合、单调 revision 和内容摘要，互不影响；
// 进程默认目录由 init() 自注册填充，派生目录用于按节点或作用域裁剪可见工具。
type Catalog struct {
	mu       sync.RWMutex
	tools    []Tool
	revision atomic.Uint64
}

// NewCatalog 创建一个空的工具目录。
func NewCatalog() *Catalog {
	return &Catalog{}
}

// Register 注册一个工具；名称为空或与已有工具重名时返回错误。
// 注册时深拷贝工具定义，注册方之后修改原始 schema 不会影响目录。
func (c *Catalog) Register(tool Tool) error {
	if tool.Name == "" {
		return fmt.Errorf("executor: tool name cannot be empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.tools {
		if c.tools[i].Name == tool.Name {
			return fmt.Errorf("executor: duplicate tool registration: %s", tool.Name)
		}
	}
	c.tools = append(c.tools, cloneTool(tool))
	c.revision.Add(1)
	return nil
}

// MustRegister 注册一个工具，失败时 panic；供 init() 自注册使用。
func (c *Catalog) MustRegister(tool Tool) {
	if err := c.Register(tool); err != nil {
		panic(err.Error())
	}
}

// Find 按名称查找工具，返回深拷贝。
func (c *Catalog) Find(name string) (Tool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.tools {
		if c.tools[i].Name == name {
			return cloneTool(c.tools[i]), true
		}
	}
	return Tool{}, false
}

// Tools 返回目录中全部工具的深拷贝。
func (c *Catalog) Tools() []Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tools := make([]Tool, len(c.tools))
	for i := range c.tools {
		tools[i] = cloneTool(c.tools[i])
	}
	return tools
}

// Snapshot 返回深拷贝工具定义、单调 revision 和规范摘要。
func (c *Catalog) Snapshot() ToolRegistrySnapshot {
	c.mu.RLock()
	tools := make([]Tool, len(c.tools))
	for i := range c.tools {
		tools[i] = cloneTool(c.tools[i])
	}
	revision := c.revision.Load()
	c.mu.RUnlock()
	return ToolRegistrySnapshot{Revision: revision, Tools: tools, Digest: catalogDigest(tools)}
}

// Derive 返回一个新目录，只包含 keep 返回 true 的工具。
// keep 为 nil 时复制全部工具。派生目录与源目录完全独立。
func (c *Catalog) Derive(keep func(Tool) bool) *Catalog {
	derived := NewCatalog()
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.tools {
		if keep != nil && !keep(c.tools[i]) {
			continue
		}
		derived.tools = append(derived.tools, cloneTool(c.tools[i]))
		derived.revision.Add(1)
	}
	return derived
}

func catalogDigest(tools []Tool) string {
	type digestTool struct {
		Name           string
		Description    string
		Parameters     map[string]any
		DefaultConfirm bool
		OwnsConfirm    bool
	}
	digestTools := make([]digestTool, len(tools))
	for i := range tools {
		digestTools[i] = digestTool{
			Name: tools[i].Name, Description: tools[i].Description,
			Parameters:     tools[i].SchemaParameters(),
			DefaultConfirm: tools[i].DefaultConfirm, OwnsConfirm: tools[i].OwnsConfirm,
		}
	}
	sort.Slice(digestTools, func(i, j int) bool { return digestTools[i].Name < digestTools[j].Name })
	encoded, _ := json.Marshal(digestTools)
	digest := sha256.Sum256(append([]byte("half-pi:tool-registry:v1\x00"), encoded...))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func cloneTool(tool Tool) Tool {
	if tool.Parameters != nil {
		parameters := *tool.Parameters
		parameters.Properties = append([]PropertySchema(nil), tool.Parameters.Properties...)
		parameters.Required = append([]string(nil), tool.Parameters.Required...)
		tool.Parameters = &parameters
	}
	return tool
}

// ── 进程默认目录 ──

var defaultCatalog = NewCatalog()

// DefaultCatalog 返回由 init() 自注册填充的进程默认工具目录。
func DefaultCatalog() *Catalog { return defaultCatalog }

// Register 在 init() 中调用，注册工具到进程默认目录。
func Register(t Tool) { defaultCatalog.MustRegister(t) }

// RegisteredTools 返回进程默认目录中的所有工具。
func RegisteredTools() []Tool { return defaultCatalog.Tools() }

// RegisteredToolsSnapshot 返回进程默认目录的深拷贝、revision 和摘要。
func RegisteredToolsSnapshot() ToolRegistrySnapshot { return defaultCatalog.Snapshot() }

// FindTool 按名称在进程默认目录中查找工具。
func FindTool(name string) (Tool, bool) { return defaultCatalog.Find(name) }
