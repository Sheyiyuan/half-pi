// Package skill 管理技能文件的加载、缓存和查询。
// 技能是 frontmatter + markdown 格式的文件，放 ~/.half-pi/skills/。
package skill

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
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
	Groups      []string // 允许使用此技能的 SessionGroup；空表示全局共享
	Always      bool     // 在可见 group 下无条件激活，不依赖模型判断
}

// Skill 是一个完整的技能定义。
type Skill struct {
	Meta
	Content  string // frontmatter 之后的 markdown 正文
	FilePath string // 源文件路径
}

// SkillSummary 是不含正文的技能摘要。
type SkillSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Always      bool     `json:"always,omitempty"`
}

// Store 管理已加载的技能。
type Store struct {
	skills   map[string]*Skill
	warnings []string
	mu       sync.RWMutex
	dir      string
	revision uint64
}

// Snapshot 是 Skill Store 在一个 revision 上的不可变规范视图。
type Snapshot struct {
	Revision uint64
	Skills   []Skill
	Digest   string
}

// LoadFromDir 递归扫描目录下所有 *.skill.md 文件并加载。
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
	next := make(map[string]*Skill)
	var warnings []string
	paths, err := scanSkillFiles(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			s.skills, s.warnings = next, nil
			s.revision++
			return nil
		}
		return fmt.Errorf("failed to read skill directory: %w", err)
	}

	for _, path := range paths {
		sk, parseErr := parseFile(path)
		if parseErr != nil {
			// parseFile 的错误已带路径，这里不再重复拼接。
			warnings = append(warnings, fmt.Sprintf("skipped %v", parseErr))
			continue
		}
		// paths 已按路径排序，重名时第一个生效，保证加载结果确定。
		if existing, ok := next[sk.Name]; ok {
			warnings = append(warnings, fmt.Sprintf("duplicate skill %q: %s shadowed by %s", sk.Name, path, existing.FilePath))
			continue
		}
		next[sk.Name] = sk
	}
	s.skills, s.warnings = next, warnings
	s.revision++
	return nil
}

// scanSkillFiles 递归收集 *.skill.md，跳过隐藏目录和常见缓存目录，返回按路径排序的结果。
func scanSkillFiles(dir string) ([]string, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	var paths []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// 单个子树不可读不应中断整体加载。
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != dir && skipSkillDir(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".skill.md") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func skipSkillDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "__pycache__", "vendor", "target", "dist", "build":
		return true
	}
	return false
}

// Warnings 返回最近一次加载中跳过的文件和重名冲突。
func (s *Store) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

// List 返回所有已加载技能，按名称排序。
func (s *Store) List() []*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Skill, 0, len(s.skills))
	for _, sk := range s.skills {
		copy := cloneSkill(sk)
		result = append(result, &copy)
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
	if !ok {
		return nil, false
	}
	copy := cloneSkill(sk)
	return &copy, true
}

// Snapshot 返回技能定义的深拷贝、单调 revision 和规范摘要。
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	skills := make([]Skill, 0, len(s.skills))
	for _, current := range s.skills {
		skills = append(skills, cloneSkill(current))
	}
	revision := s.revision
	s.mu.RUnlock()
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	type digestSkill struct {
		Name, Description, Version, Author, Content string
		Tags, Groups                                []string
		Always                                      bool
	}
	digestSkills := make([]digestSkill, len(skills))
	for i := range skills {
		digestSkills[i] = digestSkill{
			Name: skills[i].Name, Description: skills[i].Description,
			Version: skills[i].Version, Author: skills[i].Author, Content: skills[i].Content,
			Tags: append([]string(nil), skills[i].Tags...), Groups: append([]string(nil), skills[i].Groups...),
			Always: skills[i].Always,
		}
	}
	encoded, _ := json.Marshal(digestSkills)
	digest := sha256.Sum256(append([]byte("half-pi:skill-store:v1\x00"), encoded...))
	return Snapshot{Revision: revision, Skills: skills, Digest: fmt.Sprintf("sha256:%x", digest[:])}
}

func cloneSkill(skill *Skill) Skill {
	if skill == nil {
		return Skill{}
	}
	copy := *skill
	copy.Tags = append([]string(nil), skill.Tags...)
	copy.Groups = append([]string(nil), skill.Groups...)
	return copy
}

// GetForGroup 按名称查询当前 SessionGroup 可见的技能。
func (s *Store) GetForGroup(name, groupID string) (*Skill, bool) {
	sk, ok := s.Get(name)
	if !ok || !skillVisibleToGroup(sk, groupID) {
		return nil, false
	}
	return sk, true
}

// Index 生成技能的索引文本，用于注入 system prompt。
func (s *Store) Index() string {
	return s.IndexForGroup("")
}

// IndexForGroup 生成指定 SessionGroup 可见的技能索引。
func (s *Store) IndexForGroup(groupID string) string {
	list := s.visibleToGroup(groupID)
	if len(list) == 0 {
		return ""
	}
	var buf strings.Builder
	buf.WriteString("可用技能：\n")
	for _, sk := range list {
		buf.WriteString(fmt.Sprintf("  %-20s — %s", sk.Name, sk.Description))
		if sk.Always {
			buf.WriteString(" [始终激活]")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("\n查看技能详情：view_skill(\"<name>\")")
	return buf.String()
}

// SummariesForGroup 返回指定 SessionGroup 可见的技能摘要，不含正文。
func (s *Store) SummariesForGroup(groupID string) []SkillSummary {
	list := s.visibleToGroup(groupID)
	summaries := make([]SkillSummary, 0, len(list))
	for _, sk := range list {
		summaries = append(summaries, SkillSummary{
			Name: sk.Name, Description: sk.Description,
			Tags: append([]string(nil), sk.Tags...), Always: sk.Always,
		})
	}
	return summaries
}

// visibleToGroup 过滤当前 group 可见技能，按 always 优先、name 次之稳定排序。
func (s *Store) visibleToGroup(groupID string) []*Skill {
	all := s.List()
	list := make([]*Skill, 0, len(all))
	for _, sk := range all {
		if skillVisibleToGroup(sk, groupID) {
			list = append(list, sk)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Always != list[j].Always {
			return list[i].Always
		}
		return list[i].Name < list[j].Name
	})
	return list
}

func skillVisibleToGroup(sk *Skill, groupID string) bool {
	if sk == nil || len(sk.Groups) == 0 {
		return sk != nil
	}
	if groupID == "" {
		return false
	}
	for _, allowed := range sk.Groups {
		if allowed == groupID {
			return true
		}
	}
	return false
}
