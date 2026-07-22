package compact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

const (
	maxToolProjectionBytes = 4 << 10
	maxFactStringBytes     = 256
	maxFactArrayItems      = 64
)

var sha256DigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ToolFactInput 是中央 projector 接受的未可信工具结果候选。
type ToolFactInput struct {
	Tool           string
	Success        bool
	ReasonCategory string
	OutputBytes    int
	OutputDigest   string
	CandidateFacts map[string]any
}

// ToolFactProjector 将工具候选收紧为可持久化的规范投影。
type ToolFactProjector struct{}

type storedToolProjection struct {
	Facts          map[string]any `json:"facts"`
	OutputBytes    int            `json:"output_bytes"`
	OutputDigest   string         `json:"output_digest"`
	ReasonCategory string         `json:"reason_category"`
	Success        bool           `json:"success"`
	Tool           string         `json:"tool"`
	Version        string         `json:"version"`
}

type projectedMessage struct {
	Seq   int                  `json:"seq"`
	Role  string               `json:"role"`
	Text  string               `json:"text,omitempty"`
	Tools []projectedToolCall  `json:"tools,omitempty"`
	Tool  *projectedToolResult `json:"tool,omitempty"`
}

type projectedToolCall struct {
	Order int    `json:"order"`
	Tool  string `json:"tool"`
}

type projectedToolResult struct {
	Order          int            `json:"order"`
	Tool           string         `json:"tool"`
	Omitted        bool           `json:"omitted"`
	Success        *bool          `json:"success,omitempty"`
	ReasonCategory string         `json:"reason_category,omitempty"`
	OutputBytes    *int           `json:"output_bytes,omitempty"`
	Facts          map[string]any `json:"facts,omitempty"`
}

type toolCallBinding struct {
	tool  string
	order int
}

// ProjectToolResult 生成与实际持久化工具文本绑定的规范 JSON；策略失败时返回合法 omit 投影。
func ProjectToolResult(tool string, result *executor.ToolResult, persistedOutput string) string {
	if result == nil {
		return mustOmitProjection(tool, persistedOutput)
	}
	candidates := factsFromExecutor(result.CompactFacts)
	input := ToolFactInput{
		Tool: tool, Success: result.Success, ReasonCategory: result.CompactReason,
		OutputBytes: len(persistedOutput), OutputDigest: contentDigest(persistedOutput), CandidateFacts: candidates,
	}
	projected, err := (ToolFactProjector{}).ProjectToolResult(input)
	if err != nil {
		return mustOmitProjection(tool, persistedOutput)
	}
	return string(projected)
}

// ProjectToolResult 应用中央字段策略并返回规范 JSON。
func (ToolFactProjector) ProjectToolResult(input ToolFactInput) (json.RawMessage, error) {
	if input.Tool == "" || len(input.Tool) > 128 || input.OutputBytes < 0 || !sha256DigestPattern.MatchString(input.OutputDigest) {
		return nil, fmt.Errorf("invalid tool projection metadata")
	}
	if _, registered := executor.FindTool(input.Tool); !registered {
		input.CandidateFacts = nil
	}
	reason := normalizeReason(input.Success, input.ReasonCategory)
	facts := projectFacts(input.Tool, input.CandidateFacts)
	projection := storedToolProjection{
		Version: ToolProjectionVersion, Tool: input.Tool, Success: input.Success,
		ReasonCategory: reason, OutputBytes: input.OutputBytes, OutputDigest: input.OutputDigest,
		Facts: facts,
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return nil, fmt.Errorf("encode tool projection: %w", err)
	}
	if len(encoded) > maxToolProjectionBytes {
		projection.Facts = map[string]any{}
		encoded, err = json.Marshal(projection)
	}
	if err != nil || len(encoded) > maxToolProjectionBytes {
		return nil, fmt.Errorf("tool projection exceeds limit")
	}
	return encoded, nil
}

func projectSource(messages []store.Message, fromSeq, toSeq int) ([]projectedMessage, error) {
	if fromSeq < 1 || toSeq < fromSeq {
		return nil, fmt.Errorf("invalid source projection range")
	}
	bindings := make(map[string]toolCallBinding)
	result := make([]projectedMessage, 0, toSeq-fromSeq+1)
	for _, message := range messages {
		if message.Seq < fromSeq || message.Seq > toSeq {
			continue
		}
		switch message.Role {
		case string(llm.RoleSystem):
			continue
		case string(llm.RoleUser), string(llm.RoleAssistant):
			record := projectedMessage{Seq: message.Seq, Role: message.Role}
			if message.Content != "" {
				record.Text = security.RedactSensitiveText(message.Content).Text
			}
			if message.Role == string(llm.RoleAssistant) {
				calls, ok := parseStoredToolCalls(message.ToolCalls)
				if !ok {
					return nil, fmt.Errorf("invalid stored tool calls")
				}
				for index, call := range calls {
					toolName := boundedIdentifier(call.Name)
					if toolName == "" {
						toolName = "unknown"
					}
					record.Tools = append(record.Tools, projectedToolCall{Order: index + 1, Tool: toolName})
					if call.ID != "" {
						bindings[call.ID] = toolCallBinding{tool: toolName, order: index + 1}
					}
				}
			}
			result = append(result, record)
		case string(llm.RoleTool):
			binding, ok := bindings[message.ToolID]
			if !ok {
				binding = toolCallBinding{tool: "unknown", order: 1}
			}
			result = append(result, projectedMessage{
				Seq: message.Seq, Role: message.Role,
				Tool: providerToolProjection(binding, message),
			})
		default:
			return nil, fmt.Errorf("invalid stored message role")
		}
	}
	return result, nil
}

func providerToolProjection(binding toolCallBinding, message store.Message) *projectedToolResult {
	omit := &projectedToolResult{Order: binding.order, Tool: binding.tool, Omitted: true}
	if _, registered := executor.FindTool(binding.tool); !registered {
		return omit
	}
	projection, err := decodeStoredToolProjection(message.CompactProjection)
	if err != nil || projection.Tool != binding.tool || projection.OutputBytes != len(message.Content) ||
		projection.OutputDigest != contentDigest(message.Content) {
		return omit
	}
	success := projection.Success
	outputBytes := projection.OutputBytes
	return &projectedToolResult{
		Order: binding.order, Tool: binding.tool, Omitted: true,
		Success: &success, ReasonCategory: projection.ReasonCategory, OutputBytes: &outputBytes,
		Facts: cloneFacts(projection.Facts),
	}
}

func decodeStoredToolProjection(raw string) (storedToolProjection, error) {
	if raw == "" || len(raw) > maxToolProjectionBytes {
		return storedToolProjection{}, fmt.Errorf("missing tool projection")
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var projection storedToolProjection
	if err := decoder.Decode(&projection); err != nil {
		return storedToolProjection{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return storedToolProjection{}, fmt.Errorf("trailing tool projection data")
	}
	canonical, err := json.Marshal(projection)
	if err != nil || !bytes.Equal(canonical, []byte(raw)) {
		return storedToolProjection{}, fmt.Errorf("tool projection is not canonical")
	}
	if projection.Version != ToolProjectionVersion || projection.Tool == "" || projection.OutputBytes < 0 ||
		!sha256DigestPattern.MatchString(projection.OutputDigest) ||
		projection.ReasonCategory != normalizeReason(projection.Success, projection.ReasonCategory) ||
		!validStoredFacts(projection.Facts) {
		return storedToolProjection{}, fmt.Errorf("invalid tool projection")
	}
	return projection, nil
}

func normalizeReason(success bool, reason string) string {
	if success {
		return ""
	}
	switch reason {
	case "denied", "cancelled", "timed_out", "not_found", "failed":
		return reason
	default:
		return "failed"
	}
}

func factsFromExecutor(candidates []executor.CompactFact) map[string]any {
	result := make(map[string]any)
	var paths, handIDs, toolNames []string
	for _, candidate := range candidates {
		if candidate.Path != "" {
			paths = append(paths, candidate.Path)
		}
		if candidate.HandID != "" {
			handIDs = append(handIDs, candidate.HandID)
			result["hand_id"] = candidate.HandID
		}
		for key, value := range map[string]string{
			"run_id": candidate.RunID, "task_id": candidate.TaskID, "tool": candidate.Tool,
			"status": candidate.Status, "exit_category": candidate.ExitCategory,
		} {
			if value != "" {
				result[key] = value
			}
		}
		if candidate.Count >= 0 && candidate.Kind == "count" {
			result["count"] = candidate.Count
		}
		if candidate.Truncated {
			result["truncated"] = true
		}
		toolNames = append(toolNames, candidate.ToolNames...)
	}
	if len(paths) > 0 {
		result["paths"] = paths
	}
	if len(handIDs) > 1 {
		result["hand_ids"] = handIDs
	}
	if len(toolNames) > 0 {
		result["tool_names"] = toolNames
	}
	return result
}

func projectFacts(tool string, candidates map[string]any) map[string]any {
	result := make(map[string]any)
	allowStrings := map[string]bool{}
	allowArrays := map[string]bool{}
	allowCount, allowTruncated := false, false
	switch tool {
	case "use_hand", "get_hand_task", "cancel_hand_task", "read_hand_task_log":
		for _, key := range []string{"hand_id", "run_id", "task_id", "tool", "status"} {
			allowStrings[key] = true
		}
	case "select_hand":
		allowStrings["hand_id"] = true
	case "list_hands", "get_hand_info":
		allowStrings["hand_id"] = true
		allowArrays["hand_ids"], allowArrays["tool_names"] = true, true
	case "read_file", "write_file", "edit_file", "grep", "grep_regex", "list_files":
		allowArrays["paths"] = true
		allowCount, allowTruncated = true, true
	case "exec_command":
		allowStrings["exit_category"] = true
		allowTruncated = true
	}
	for key := range allowStrings {
		if value, ok := candidates[key].(string); ok && validFactString(value) && !security.IsSensitiveFieldName(key) {
			if key != "status" || validPublicStatus(value) {
				result[key] = value
			}
		}
	}
	for key := range allowArrays {
		values, ok := stringSlice(candidates[key])
		if !ok {
			continue
		}
		filtered := make([]string, 0, len(values))
		for _, value := range values {
			valid := validFactString(value)
			if key == "paths" {
				valid = validWorkspacePath(value)
			}
			if valid {
				filtered = append(filtered, value)
			}
			if len(filtered) == maxFactArrayItems {
				break
			}
		}
		if len(filtered) > 0 {
			sort.Strings(filtered)
			result[key] = uniqueStrings(filtered)
		}
	}
	if allowCount {
		if count, ok := integerValue(candidates["count"]); ok && count >= 0 {
			result["count"] = count
		}
	}
	if allowTruncated {
		if truncated, ok := candidates["truncated"].(bool); ok {
			result["truncated"] = truncated
		}
	}
	return result
}

func validStoredFacts(facts map[string]any) bool {
	if facts == nil || len(facts) > 32 {
		return false
	}
	for key, value := range facts {
		if key == "" || len(key) > 64 || security.IsSensitiveFieldName(key) {
			return false
		}
		switch typed := value.(type) {
		case string:
			if !validFactString(typed) {
				return false
			}
		case bool:
		case json.Number:
			if _, err := typed.Int64(); err != nil {
				return false
			}
		case []any:
			if len(typed) > maxFactArrayItems {
				return false
			}
			for _, item := range typed {
				text, ok := item.(string)
				if !ok || !validFactString(text) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func validFactString(value string) bool {
	if value == "" || len(value) > maxFactStringBytes || !utf8.ValidString(value) || security.RedactSensitiveText(value).Found {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validWorkspacePath(value string) bool {
	if value == "" || len(value) > 512 || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) > 32 {
		return false
	}
	for _, part := range parts {
		lower := strings.ToLower(part)
		if part == "" || part == "." || security.IsSensitiveFieldName(lower) || lower == ".env" || lower == ".ssh" ||
			strings.HasPrefix(lower, "id_rsa") || strings.Contains(lower, "credential") {
			return false
		}
	}
	return validFactString(value)
}

func validPublicStatus(value string) bool {
	switch value {
	case "pending", "sent", "accepted", "running", "succeeded", "failed", "cancelled", "timed_out", "rejected", "lost", "stale", "unknown":
		return true
	default:
		return false
	}
}

func stringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), len(typed) <= maxFactArrayItems
	case []any:
		if len(typed) > maxFactArrayItems {
			return nil, false
		}
		result := make([]string, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result[index] = text
		}
		return result, true
	default:
		return nil, false
	}
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		integer, err := typed.Int64()
		return integer, err == nil
	default:
		return 0, false
	}
}

func mustOmitProjection(tool, output string) string {
	if tool == "" {
		tool = "unknown"
	}
	tool = boundedIdentifier(tool)
	if tool == "" {
		tool = "unknown"
	}
	encoded, _ := json.Marshal(storedToolProjection{
		Version: ToolProjectionVersion, Tool: tool, Success: false,
		ReasonCategory: "failed", OutputBytes: len(output), OutputDigest: contentDigest(output), Facts: map[string]any{},
	})
	return string(encoded)
}

func contentDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func boundedIdentifier(value string) string {
	if !validFactString(value) || len(value) > 128 {
		return ""
	}
	return value
}

func cloneFacts(facts map[string]any) map[string]any {
	result := make(map[string]any, len(facts))
	for key, value := range facts {
		switch typed := value.(type) {
		case []any:
			result[key] = append([]any(nil), typed...)
		default:
			result[key] = typed
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
