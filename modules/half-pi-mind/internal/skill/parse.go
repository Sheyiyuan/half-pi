package skill

import (
	"fmt"
	"os"
	"strings"
)

func parseFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill file: %w", err)
	}

	content := string(data)
	meta, body, err := parseFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if meta.Name == "" {
		return nil, fmt.Errorf("%s: frontmatter is missing the name field", path)
	}

	return &Skill{
		Meta:     *meta,
		Content:  body,
		FilePath: path,
	}, nil
}

func parseFrontmatter(src string) (*Meta, string, error) {
	src = strings.TrimSpace(src)
	if !strings.HasPrefix(src, "---") {
		return nil, "", fmt.Errorf("skill file must start with ---")
	}

	end := strings.Index(src[3:], "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter closing --- not found")
	}

	fm := strings.TrimSpace(src[3 : 3+end])
	body := strings.TrimSpace(src[3+end+4:])

	meta := &Meta{}
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := parseKV(line)
		if !ok {
			continue
		}
		switch key {
		case "name":
			meta.Name = val
		case "description":
			meta.Description = val
		case "tags":
			meta.Tags = parseTags(val)
		case "version":
			meta.Version = val
		case "author":
			meta.Author = val
		}
	}

	return meta, body, nil
}

func parseKV(line string) (key, val string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	val = strings.TrimSpace(line[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

func parseTags(s string) []string {
	s = strings.Trim(s, "[]")
	if s == "" {
		return nil
	}
	var tags []string
	for _, tag := range strings.Split(s, ",") {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}
