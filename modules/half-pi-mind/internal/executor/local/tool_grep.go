package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
)

const (
	grepMaxMatches   = 200
	grepMaxFileSize  = 1 << 20
	grepContextLines = 1
)

func init() {
	executor.Register(executor.Tool{
		Name:        "grep",
		Description: "Search files for a literal string (not interpreted as regex). Returns matching lines with context.",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "pattern", Type: "string", Description: "The string to search for"},
				{Name: "path", Type: "string", Description: "File or directory to search. Defaults to current directory"},
				{Name: "include", Type: "string", Description: "Glob pattern to filter filenames, e.g. \"*.go\". No filter by default"},
				{Name: "max_matches", Type: "number", Description: "Maximum number of matches. Default 200"},
			},
			Required: []string{"pattern"},
		},
		Execute: grepExecute,
	})
}

func grepExecute(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	var p struct {
		Pattern    string  `json:"pattern"`
		Path       string  `json:"path"`
		Include    string  `json:"include"`
		MaxMatches float64 `json:"max_matches"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
	}
	if p.Pattern == "" {
		return &executor.ToolResult{Error: "pattern is required"}
	}
	if p.Path == "" {
		p.Path = "."
	}
	maxMatches := int(p.MaxMatches)
	if maxMatches <= 0 {
		maxMatches = grepMaxMatches
	}

	searchPattern := p.Pattern
	results, totalMatches, filesScanned, limited, err := searchFiles(p.Path, p.Include, maxMatches, func(path, content string) []matchResult {
		return matchLiteral(path, content, searchPattern)
	})
	if err != nil {
		return &executor.ToolResult{Error: err.Error()}
	}

	header := fmt.Sprintf("Search: pattern=%q", p.Pattern)
	if p.Include != "" {
		header += fmt.Sprintf(" include=%q", p.Include)
	}
	header += fmt.Sprintf("  Matches: %d  Files scanned: %d", totalMatches, filesScanned)
	if limited {
		header += fmt.Sprintf("  [limit reached: %d]", maxMatches)
	}
	header += "\n\n"

	if len(results) == 0 {
		header += "(no matches)\n"
	}

	return &executor.ToolResult{Success: true, Output: header + strings.Join(results, "\n") + "\n"}
}

type matchResult struct {
	output     string
	matchCount int
}

func searchFiles(root, include string, maxMatches int, matchFn func(path, content string) []matchResult) (results []string, totalMatches, filesScanned int, limited bool, err error) {
	info, statErr := os.Stat(root)
	if statErr != nil {
		return nil, 0, 0, false, fmt.Errorf("cannot access %s: %w", root, statErr)
	}

	if !info.IsDir() {
		return searchSingleFile(root, maxMatches, matchFn)
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return fs.SkipDir
			}
			return nil
		}

		if include != "" {
			matched, _ := filepath.Match(include, d.Name())
			if !matched {
				return nil
			}
		}

		info, _ := d.Info()
		if info.Size() > grepMaxFileSize {
			return nil
		}

		filesScanned++

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		if isBinary(data) {
			return nil
		}

		for _, mr := range matchFn(path, string(data)) {
			totalMatches += mr.matchCount
			results = append(results, mr.output)
			if len(results) >= maxMatches {
				limited = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, 0, 0, false, fmt.Errorf("failed to walk directory: %w", walkErr)
	}
	return
}

func searchSingleFile(path string, maxMatches int, matchFn func(path, content string) []matchResult) (results []string, totalMatches, filesScanned int, limited bool, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return nil, 0, 0, false, fmt.Errorf("cannot access %s: %w", path, statErr)
	}
	if info.Size() > grepMaxFileSize {
		return nil, 0, 0, false, fmt.Errorf("file too large: %s (%d bytes)", path, info.Size())
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, 0, 0, false, fmt.Errorf("failed to read file %s: %w", path, readErr)
	}

	if isBinary(data) {
		return nil, 0, 0, false, fmt.Errorf("skipping binary file: %s", path)
	}

	filesScanned = 1
	for _, mr := range matchFn(path, string(data)) {
		totalMatches += mr.matchCount
		results = append(results, mr.output)
		if len(results) >= maxMatches {
			limited = true
			break
		}
	}
	return
}

func matchLiteral(path, content, pattern string) []matchResult {
	lines := splitLines(content)
	matched := make([]bool, len(lines))
	matchCount := 0

	for i, line := range lines {
		if strings.Contains(line, pattern) {
			matched[i] = true
			matchCount++
		}
	}

	return []matchResult{{output: formatMatches(path, lines, matched), matchCount: matchCount}}
}

func formatMatches(path string, lines []string, matched []bool) string {
	var buf strings.Builder
	buf.WriteString(path + ":\n")
	for i := 0; i < len(lines); i++ {
		if !matched[i] {
			continue
		}
		ctxStart := i - grepContextLines
		if ctxStart < 0 {
			ctxStart = 0
		}
		ctxEnd := i + grepContextLines
		if ctxEnd >= len(lines) {
			ctxEnd = len(lines) - 1
		}

		for j := ctxStart; j <= ctxEnd; j++ {
			prefix := "    "
			if j == i {
				prefix = "   →"
			}
			buf.WriteString(fmt.Sprintf("%s %s\n", prefix, formatSearchLine(j+1, lines[j])))
		}
		buf.WriteString("\n")
	}
	return buf.String()
}

func isBinary(data []byte) bool {
	limit := len(data)
	if limit > 8000 {
		limit = 8000
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}

func formatSearchLine(lineno int, content string) string {
	return fmt.Sprintf("%6d  %s", lineno, content)
}
