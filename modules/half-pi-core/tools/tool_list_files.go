// 列出目录内容，支持递归遍历和 glob 过滤。
// 输出格式：权限 大小  路径。按名称排序，目录末尾带 /。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "list_files",
		Description: "列出文件和目录，支持递归遍历和 glob 过滤",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "path", Type: "string", Description: "目录路径，默认为当前目录"},
				{Name: "recursive", Type: "boolean", Description: "是否递归列出子目录，默认 false"},
				{Name: "pattern", Type: "string", Description: "glob 过滤模式，如 \"*.go\"。默认不过滤"},
			},
		},
		Execute: listFilesExecute,
	})
}

func listFilesExecute(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	var p struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Pattern   string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
	}
	if p.Path == "" {
		p.Path = "."
	}

	var entries []fileEntry
	var err error
	if p.Recursive {
		entries, err = walkFiles(p.Path, p.Pattern)
	} else {
		entries, err = readDir(p.Path, p.Pattern)
	}
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to read directory: %v", err)}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("[目录: %s  文件数: %d", p.Path, len(entries)))
	if p.Recursive {
		buf.WriteString("  recursive")
	}
	if p.Pattern != "" {
		buf.WriteString(fmt.Sprintf("  pattern: %s", p.Pattern))
	}
	buf.WriteString("]\n")

	if len(entries) == 0 {
		buf.WriteString("(无匹配文件)\n")
	} else {
		sep := strings.Repeat("-", 60)
		buf.WriteString(sep + "\n")
		for _, e := range entries {
			name := e.path
			if e.isDir {
				name += "/"
			}
			buf.WriteString(fmt.Sprintf("%s %8d  %s\n", e.mode, e.size, name))
		}
		buf.WriteString(sep + "\n")
	}

	return &executor.ToolResult{Success: true, Output: buf.String()}
}

type fileEntry struct {
	path  string
	size  int64
	mode  string
	isDir bool
}

func readDir(root string, pattern string) ([]fileEntry, error) {
	dirEntries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var entries []fileEntry
	for _, de := range dirEntries {
		info, _ := de.Info()
		name := filepath.Join(root, de.Name())
		if pattern != "" {
			if !de.IsDir() {
				matched, _ := filepath.Match(pattern, de.Name())
				if !matched {
					continue
				}
			}
		}
		entries = append(entries, fileEntry{
			path:  name,
			size:  info.Size(),
			mode:  info.Mode().String(),
			isDir: de.IsDir(),
		})
	}
	return entries, nil
}

func walkFiles(root string, pattern string) ([]fileEntry, error) {
	var entries []fileEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的目录
		}
		if path == root {
			return nil
		}
		if pattern != "" {
			if !d.IsDir() {
				matched, _ := filepath.Match(pattern, d.Name())
				if !matched {
					return nil
				}
			}
		}
		info, _ := d.Info()
		entries = append(entries, fileEntry{
			path:  path,
			size:  info.Size(),
			mode:  info.Mode().String(),
			isDir: d.IsDir(),
		})
		return nil
	})
	return entries, err
}
