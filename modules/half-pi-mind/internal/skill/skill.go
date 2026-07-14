// Package skill 管理技能文件的加载、缓存和查询。
// 技能是 frontmatter + markdown 格式的文件，放 ~/.half-pi/skills/。
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Meta 是技能文件的 frontmatter 元信息。
type Meta struct {
	Name        string   // 唯一标识
	Description string   // LLM 据此判断技能用途
	Tags        []string // 分类标签
	Version     string   // 版本号
	Author      string   // 创建者
}

// Skill 是一个完整的技能定义。
type Skill struct {
	Meta
	Content  string // frontmatter 之后的 markdown 正文
	FilePath string // 源文件路径
}

// Store 管理已加载的技能。
type Store struct {
	skills map[string]*Skill
	mu     sync.RWMutex
	dir    string
}

// LoadFromDir 扫描目录下所有 *.skill.md 文件并加载。
func LoadFromDir(dir string) (*Store, error) {
	s := &Store{
		skills: make(map[string]*Skill),
		dir:    dir,
	}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload 重新扫描技能目录。
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reload()
}

func (s *Store) reload() error {
	s.skills = make(map[string]*Skill)

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read skill directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".skill.md") {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		sk, parseErr := parseFile(path)
		if parseErr != nil {
			continue
		}
		s.skills[sk.Name] = sk
	}
	return nil
}

// List 返回所有已加载技能，按名称排序。
func (s *Store) List() []*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Skill, 0, len(s.skills))
	for _, sk := range s.skills {
		result = append(result, sk)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Get 按名称查找技能。
func (s *Store) Get(name string) (*Skill, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sk, ok := s.skills[name]
	return sk, ok
}

// Index 生成技能的索引文本，用于注入 system prompt。
func (s *Store) Index() string {
	list := s.List()
	if len(list) == 0 {
		return ""
	}
	var buf strings.Builder
	buf.WriteString("可用技能：\n")
	for _, sk := range list {
		buf.WriteString(fmt.Sprintf("  %-20s — %s\n", sk.Name, sk.Description))
	}
	buf.WriteString("\n查看技能详情：view_skill(\"<name>\")")
	return buf.String()
}
