package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantDesc string
		wantErr  bool
	}{
		{
			name: "valid frontmatter",
			input: `---
name: go-patterns
description: Go project conventions
tags: [go, coding]
version: 1.0.0
author: test
---

# Go Patterns

Content here.`,
			wantName: "go-patterns",
			wantDesc: "Go project conventions",
		},
		{
			name: "minimal frontmatter",
			input: `---
name: minimal
description: Minimal skill
---

Content`,
			wantName: "minimal",
			wantDesc: "Minimal skill",
		},
		{
			name:    "no frontmatter",
			input:   "# Just markdown\n\nNo frontmatter here.",
			wantErr: true,
		},
		{
			name: "missing name field",
			input: `---
description: No name field
---

Content`,
			wantErr: false,
			// Meta defaults to empty, parseFile returns error for missing name
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, body, err := parseFrontmatter(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantName != "" && meta.Name != tt.wantName {
				t.Errorf("name = %q, want %q", meta.Name, tt.wantName)
			}
			if tt.wantDesc != "" && meta.Description != tt.wantDesc {
				t.Errorf("description = %q, want %q", meta.Description, tt.wantDesc)
			}
			if body == "" && tt.input == "" {
				// empty content is ok
			}
		})
	}
}

func TestParseTags(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"[go, coding, deploy]", []string{"go", "coding", "deploy"}},
		{"[go]", []string{"go"}},
		{"[]", nil},
		{"", nil},
		{"[a, b, c]", []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		got := parseTags(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseTags(%q) len = %d, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseTags(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestStoreLoadAndGet(t *testing.T) {
	dir := t.TempDir()

	// Create a valid skill file
	writeSkillFile(t, dir, "test.skill.md", `---
name: test-skill
description: A test skill
tags: [testing]
---

# Test Skill

This is a test skill.`)

	// Create a second skill
	writeSkillFile(t, dir, "deploy.skill.md", `---
name: deploy
description: Deployment workflow
---

# Deploy

Deploy instructions.`)

	store, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir failed: %v", err)
	}

	// Test Get
	sk, ok := store.Get("test-skill")
	if !ok {
		t.Fatal("expected to find test-skill")
	}
	if sk.Name != "test-skill" {
		t.Errorf("name = %q, want test-skill", sk.Name)
	}
	if sk.Description != "A test skill" {
		t.Errorf("description = %q", sk.Description)
	}
	if len(sk.Tags) != 1 || sk.Tags[0] != "testing" {
		t.Errorf("tags = %v, want [testing]", sk.Tags)
	}
	if sk.Content != "# Test Skill\n\nThis is a test skill." {
		t.Errorf("content = %q", sk.Content)
	}

	// Test List
	list := store.List()
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	if list[0].Name > list[1].Name {
		t.Error("list should be sorted by name")
	}

	// Test missing skill
	_, ok = store.Get("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent skill")
	}
}

func TestStoreEmptyDir(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir on empty dir failed: %v", err)
	}
	list := store.List()
	if len(list) != 0 {
		t.Errorf("empty dir should have 0 skills, got %d", len(list))
	}

	// Get on empty store
	_, ok := store.Get("anything")
	if ok {
		t.Error("Get on empty store should return false")
	}
}

func TestStoreNonexistentDir(t *testing.T) {
	store, err := LoadFromDir("/tmp/nonexistent-skill-dir-12345")
	if err != nil {
		t.Fatalf("LoadFromDir on nonexistent dir failed: %v", err)
	}
	list := store.List()
	if len(list) != 0 {
		t.Errorf("nonexistent dir should have 0 skills, got %d", len(list))
	}
}

func TestStoreSkipsInvalidFiles(t *testing.T) {
	dir := t.TempDir()

	// Missing name field
	writeSkillFile(t, dir, "noname.skill.md", `---
description: No name
---

Content.`)

	// Valid file
	writeSkillFile(t, dir, "valid.skill.md", `---
name: valid
description: Valid skill
---

Content.`)

	store, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir failed: %v", err)
	}

	list := store.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 valid skill, got %d", len(list))
	}
	if list[0].Name != "valid" {
		t.Errorf("expected 'valid' skill, got %q", list[0].Name)
	}
}

func TestStoreSkipsNonSkillFiles(t *testing.T) {
	dir := t.TempDir()

	writeSkillFile(t, dir, "valid.skill.md", `---
name: valid
description: Valid
---

Content.`)

	// Regular markdown file without .skill.md extension
	writeSkillFile(t, dir, "notes.md", "# Just notes")

	store, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir failed: %v", err)
	}

	list := store.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(list))
	}
}

func TestStoreIndex(t *testing.T) {
	dir := t.TempDir()

	writeSkillFile(t, dir, "go.skill.md", `---
name: go-patterns
description: Go coding patterns
---

# Go`)

	writeSkillFile(t, dir, "deploy.skill.md", `---
name: deploy-flow
description: Standard deploy flow
---

# Deploy`)

	store, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir failed: %v", err)
	}

	index := store.Index()
	if index == "" {
		t.Fatal("Index should not be empty")
	}

	// Index should mention both skills
	for _, name := range []string{"go-patterns", "deploy-flow"} {
		if !contains(index, name) {
			t.Errorf("Index missing skill %q", name)
		}
	}
}

func TestStoreIndexEmpty(t *testing.T) {
	dir := t.TempDir()
	store, _ := LoadFromDir(dir)
	if store.Index() != "" {
		t.Errorf("empty store Index should be empty, got %q", store.Index())
	}
}

func TestStoreFiltersSkillsBySessionGroup(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "global.skill.md", `---
name: global
description: Shared skill
---

global content`)
	writeSkillFile(t, dir, "private.skill.md", `---
name: private
description: Group A only
groups: [group-a]
---

private content`)
	store, err := LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	private, ok := store.Get("private")
	if !ok || len(private.Groups) != 1 || private.Groups[0] != "group-a" {
		t.Fatalf("parsed groups = %#v", private)
	}
	if _, ok := store.GetForGroup("private", "group-b"); ok {
		t.Fatal("foreign group obtained private skill")
	}
	if _, ok := store.GetForGroup("global", "group-b"); !ok {
		t.Fatal("global skill should remain visible")
	}
	indexA := store.IndexForGroup("group-a")
	indexB := store.IndexForGroup("group-b")
	if !strings.Contains(indexA, "private") || strings.Contains(indexB, "private") {
		t.Fatalf("group indexes leaked skill: group-a=%q group-b=%q", indexA, indexB)
	}
}

func TestStoreReload(t *testing.T) {
	dir := t.TempDir()

	writeSkillFile(t, dir, "first.skill.md", `---
name: first
description: First skill
---

Content.`)

	store, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir failed: %v", err)
	}

	if len(store.List()) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(store.List()))
	}

	// Add a new skill file
	writeSkillFile(t, dir, "second.skill.md", `---
name: second
description: Second skill
---

Content.`)

	if err := store.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	list := store.List()
	if len(list) != 2 {
		t.Fatalf("after reload expected 2 skills, got %d", len(list))
	}
}

func writeSkillFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test skill file: %v", err)
	}
}

func TestSnapshotIsImmutableAndRevisioned(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "one.skill.md", `---
name: one
description: One
tags: [first]
---
Body.`)
	store, err := LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := store.Snapshot()
	if first.Revision == 0 || len(first.Skills) != 1 || first.Digest == "" {
		t.Fatalf("first snapshot = %+v", first)
	}
	first.Skills[0].Description = "mutated"
	second := store.Snapshot()
	if second.Skills[0].Description != "One" || second.Digest != first.Digest {
		t.Fatalf("skill snapshot was not immutable: %+v", second)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	third := store.Snapshot()
	if third.Revision != second.Revision+1 || third.Digest != second.Digest {
		t.Fatalf("reload snapshot = %+v", third)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
