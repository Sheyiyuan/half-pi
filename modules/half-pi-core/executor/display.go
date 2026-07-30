package executor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// DisplayProjectionVersion 是用户工具详情投影的稳定版本。
const DisplayProjectionVersion = "tool-display.v1"

const (
	MaxDisplayArgsBytes   = 256 << 10
	MaxDisplayOutputBytes = 1 << 20
)

// DisplayExposure 定义参数字段进入用户透明视图时的处理方式。
type DisplayExposure string

const (
	DisplayShow    DisplayExposure = "show"
	DisplayMask    DisplayExposure = "mask"
	DisplayHide    DisplayExposure = "hide"
	DisplayPreview DisplayExposure = "preview"
)

// DisplayField 是一个参数字段的结构化展示结果。
type DisplayField struct {
	State     DisplayExposure `json:"state"`
	Value     any             `json:"value,omitempty"`
	Preview   string          `json:"preview,omitempty"`
	Bytes     int             `json:"bytes,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
}

// DisplayArgs 是参数展示投影，不包含安全审计所需的原始参数。
type DisplayArgs struct {
	ProjectionVersion string                  `json:"projection_version"`
	Fields            map[string]DisplayField `json:"fields"`
	Bytes             int                     `json:"bytes"`
	Truncated         bool                    `json:"truncated"`
	Warnings          []string                `json:"warnings"`
}

// DisplayOutput 是工具终态输出展示投影。
type DisplayOutput struct {
	Stdout      string   `json:"stdout,omitempty"`
	Stderr      string   `json:"stderr,omitempty"`
	StdoutBytes int      `json:"stdout_bytes"`
	StderrBytes int      `json:"stderr_bytes"`
	OutputBytes int      `json:"output_bytes"`
	Digest      string   `json:"digest"`
	Truncated   bool     `json:"truncated"`
	Warnings    []string `json:"warnings"`
}

var secretPattern = regexp.MustCompile(`(?i)(?:password|passwd|token|secret|api[_-]?key|private[_-]?key)\s*[:=]`)
var credentialPattern = regexp.MustCompile(`(?:sk-[A-Za-z0-9]{16,}|gh[pousr]_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16})`)

// ProjectDisplayArgs 根据 schema 和中央高置信规则生成参数展示投影。
func ProjectDisplayArgs(tool Tool, args json.RawMessage) (DisplayArgs, error) {
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil || values == nil {
		if err == nil {
			err = fmt.Errorf("expected object")
		}
		return DisplayArgs{}, fmt.Errorf("decode display args: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return DisplayArgs{}, fmt.Errorf("decode display args: expected one object")
	}
	fields := make(map[string]DisplayField, len(values))
	properties := make(map[string]PropertySchema)
	if tool.Parameters != nil {
		for _, property := range tool.Parameters.Properties {
			properties[property.Name] = property
		}
	}
	result := DisplayArgs{ProjectionVersion: DisplayProjectionVersion, Fields: fields, Warnings: []string{}}
	names := sortedMapKeys(values)
	for _, name := range names {
		value := values[name]
		property := properties[name]
		state, central, err := resolveDisplayState(name, property.Display)
		if err != nil {
			return DisplayArgs{}, err
		}
		projectDisplayValue(fields, &result.Warnings, name, value, state, central)
	}
	encoded, _ := json.Marshal(values)
	result.Bytes = len(encoded)
	result.Truncated = truncateDisplayFields(fields, MaxDisplayArgsBytes)
	if result.Bytes > MaxDisplayArgsBytes {
		result.Truncated = true
	}
	return result, nil
}

func projectDisplayValue(fields map[string]DisplayField, warnings *[]string, path string, value any, state DisplayExposure, central bool) {
	if central {
		*warnings = append(*warnings, "sensitive_field:"+path)
	}
	if state == DisplayShow || state == DisplayPreview {
		switch nested := value.(type) {
		case map[string]any:
			if len(nested) == 0 {
				fields[path] = displayField(state, nested)
				return
			}
			for _, name := range sortedMapKeys(nested) {
				child := nested[name]
				childPath := path + "." + name
				childState, childCentral, _ := resolveDisplayState(name, state)
				projectDisplayValue(fields, warnings, childPath, child, childState, childCentral)
			}
			return
		case []any:
			if len(nested) == 0 {
				fields[path] = displayField(state, nested)
				return
			}
			for index, child := range nested {
				childPath := fmt.Sprintf("%s[%d]", path, index)
				projectDisplayValue(fields, warnings, childPath, child, state, false)
			}
			return
		}
	}
	fields[path] = displayField(state, value)
	if scanDisplayValue(value) {
		*warnings = append(*warnings, "possible_secret:"+path)
	}
}

func resolveDisplayState(name string, declared DisplayExposure) (DisplayExposure, bool, error) {
	if declared == "" {
		declared = DisplayShow
	}
	if !validDisplayExposure(declared) {
		return "", false, fmt.Errorf("invalid display exposure %q for field %q", declared, name)
	}
	central := centralDisplayRule(name)
	if central == DisplayMask && declared != DisplayHide {
		return DisplayMask, true, nil
	}
	return declared, central != DisplayShow, nil
}

func validDisplayExposure(exposure DisplayExposure) bool {
	switch exposure {
	case DisplayShow, DisplayMask, DisplayHide, DisplayPreview:
		return true
	default:
		return false
	}
}

func truncateDisplayFields(fields map[string]DisplayField, limit int) bool {
	remaining := limit
	truncated := false
	for _, name := range sortedMapKeys(fields) {
		field := fields[name]
		switch field.State {
		case DisplayShow:
			if field.Bytes <= remaining {
				remaining -= field.Bytes
				continue
			}
			field.State = DisplayPreview
			field.Preview = previewValue(field.Value, remaining)
			field.Value = nil
			field.Truncated = true
			remaining = 0
			truncated = true
			fields[name] = field
		case DisplayPreview:
			if len(field.Preview) <= remaining {
				remaining -= len(field.Preview)
				continue
			}
			field.Preview = prefixUTF8(field.Preview, remaining)
			field.Truncated = true
			remaining = 0
			truncated = true
			fields[name] = field
		}
	}
	return truncated
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ProjectDisplayOutput 生成有界 stdout/stderr 终态投影。
func ProjectDisplayOutput(stdout, stderr string) DisplayOutput {
	result := DisplayOutput{StdoutBytes: len(stdout), StderrBytes: len(stderr), Warnings: []string{}}
	result.OutputBytes = result.StdoutBytes + result.StderrBytes
	result.Digest = digest(stdout + stderr)
	if scanDisplayValue(stdout) || scanDisplayValue(stderr) {
		result.Warnings = append(result.Warnings, "possible_secret_in_output")
	}
	safeStdout := strings.ToValidUTF8(stdout, "\uFFFD")
	safeStderr := strings.ToValidUTF8(stderr, "\uFFFD")
	if len(safeStdout) > MaxDisplayOutputBytes {
		result.Stdout, result.Truncated = prefixUTF8(safeStdout, MaxDisplayOutputBytes), true
	} else {
		result.Stdout = safeStdout
	}
	remaining := MaxDisplayOutputBytes - len(result.Stdout)
	if remaining < 0 {
		remaining = 0
	}
	if len(safeStderr) > remaining {
		result.Stderr, result.Truncated = prefixUTF8(safeStderr, remaining), true
	} else {
		result.Stderr = safeStderr
	}
	return result
}

func displayField(state DisplayExposure, value any) DisplayField {
	field := DisplayField{State: state, Bytes: valueBytes(value)}
	switch state {
	case DisplayMask:
		field.Value = "[masked]"
	case DisplayHide:
		field.Value = nil
	case DisplayPreview:
		field.Preview = previewValue(value, 256)
		field.Truncated = field.Preview != fmt.Sprint(value)
	default:
		field.State = DisplayShow
		field.Value = value
	}
	return field
}

func centralDisplayRule(name string) DisplayExposure {
	name = strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	for _, marker := range []string{"password", "passwd", "token", "secret", "api_key", "application_key", "private_key"} {
		if strings.Contains(name, marker) {
			return DisplayMask
		}
	}
	return DisplayShow
}

func scanDisplayValue(value any) bool {
	text := fmt.Sprint(value)
	return secretPattern.MatchString(text) || credentialPattern.MatchString(text)
}

func valueBytes(value any) int { data, _ := json.Marshal(value); return len(data) }

func previewValue(value any, limit int) string {
	text := fmt.Sprint(value)
	if len(text) <= limit {
		return text
	}
	return prefixUTF8(text, limit)
}

func prefixUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
