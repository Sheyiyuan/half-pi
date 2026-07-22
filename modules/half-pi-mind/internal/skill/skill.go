// Package skill 管理技能文件的加载、缓存和查询。
// 技能是 frontmatter + markdown 格式的文件，放 ~/.half-pi/skills/。
package skill

import (
	"crypto/sha256"
	"encoding/json"
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
	Groups      []string // 允许使用此技能的 SessionGroup；空表示全局共享
}

// Skill 是一个完整的技能定义。
type Skill struct {
	Meta
	Content  string // frontmatter 之后的 markdown 正文
	FilePath string // 源文件路径
}

// Store 管理已加载的技能。
type Store struct {
	skills   map[string]*Skill
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
	next := make(map[string]*Skill)
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			s.skills = next
			s.revision++
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
		next[sk.Name] = sk
	}
	s.skills = next
	s.revision++
	return nil
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
	}
	digestSkills := make([]digestSkill, len(skills))
	for i := range skills {
		digestSkills[i] = digestSkill{
			Name: skills[i].Name, Description: skills[i].Description,
			Version: skills[i].Version, Author: skills[i].Author, Content: skills[i].Content,
			Tags: append([]string(nil), skills[i].Tags...), Groups: append([]string(nil), skills[i].Groups...),
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
	all := s.List()
	list := make([]*Skill, 0, len(all))
	for _, sk := range all {
		if skillVisibleToGroup(sk, groupID) {
			list = append(list, sk)
		}
	}
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
