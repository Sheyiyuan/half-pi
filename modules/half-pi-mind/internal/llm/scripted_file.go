package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	scriptedFileVersion = 1
	maxScriptedFileSize = 1 << 20
	maxScriptedSteps    = 256
)

type scriptedFile struct {
	Version int                `json:"version"`
	Steps   []scriptedFileStep `json:"steps"`
}

type scriptedFileStep struct {
	Expect   scriptedExpectation `json:"expect,omitempty"`
	Response scriptedResponse    `json:"response"`
}

type scriptedExpectation struct {
	LastRole    Role   `json:"last_role,omitempty"`
	LastContent string `json:"last_content,omitempty"`
	LastToolID  string `json:"last_tool_id,omitempty"`
}

type scriptedResponse struct {
	Content   string             `json:"content,omitempty"`
	ToolCalls []scriptedToolCall `json:"tool_calls,omitempty"`
}

type scriptedToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// NewScriptedProviderFromFile 从严格 JSON fixture 创建确定性 Provider。
func NewScriptedProviderFromFile(path string) (*ScriptedProvider, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open scripted LLM file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxScriptedFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read scripted LLM file: %w", err)
	}
	if len(data) > maxScriptedFileSize {
		return nil, fmt.Errorf("scripted LLM file exceeds %d bytes", maxScriptedFileSize)
	}
	var fixture scriptedFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("decode scripted LLM file: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode scripted LLM file: %w", err)
	}
	steps, err := fixture.runtimeSteps()
	if err != nil {
		return nil, err
	}
	return NewScriptedProvider(steps...), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func (f scriptedFile) runtimeSteps() ([]ScriptedStep, error) {
	if f.Version != scriptedFileVersion {
		return nil, fmt.Errorf("scripted LLM version must be %d", scriptedFileVersion)
	}
	if len(f.Steps) == 0 || len(f.Steps) > maxScriptedSteps {
		return nil, fmt.Errorf("scripted LLM steps must contain between 1 and %d entries", maxScriptedSteps)
	}
	steps := make([]ScriptedStep, len(f.Steps))
	for i, source := range f.Steps {
		step, err := source.runtimeStep()
		if err != nil {
			return nil, fmt.Errorf("scripted LLM step %d: %w", i+1, err)
		}
		steps[i] = step
	}
	return steps, nil
}

func (s scriptedFileStep) runtimeStep() (ScriptedStep, error) {
	if err := s.Expect.validate(); err != nil {
		return ScriptedStep{}, err
	}
	if s.Response.Content == "" && len(s.Response.ToolCalls) == 0 {
		return ScriptedStep{}, fmt.Errorf("response content or tool_calls is required")
	}
	response := LLMResponse{Content: s.Response.Content}
	seen := make(map[string]struct{}, len(s.Response.ToolCalls))
	for _, call := range s.Response.ToolCalls {
		if call.ID == "" || call.Name == "" || len(call.Args) == 0 {
			return ScriptedStep{}, fmt.Errorf("tool call id, name, and args are required")
		}
		if _, exists := seen[call.ID]; exists {
			return ScriptedStep{}, fmt.Errorf("duplicate tool call id %q", call.ID)
		}
		seen[call.ID] = struct{}{}
		var args map[string]any
		decoder := json.NewDecoder(bytes.NewReader(call.Args))
		decoder.UseNumber()
		if err := decoder.Decode(&args); err != nil || args == nil || requireJSONEOF(decoder) != nil {
			return ScriptedStep{}, fmt.Errorf("tool call %q args must be one JSON object", call.ID)
		}
		response.ToolCalls = append(response.ToolCalls, ToolCall{ID: call.ID, Name: call.Name, Args: string(call.Args)})
	}
	expectation := s.Expect
	return ScriptedStep{
		Response: response,
		Check: func(request *LLMRequest) error {
			return expectation.check(request)
		},
	}, nil
}

func (e scriptedExpectation) validate() error {
	if e.LastRole != "" {
		switch e.LastRole {
		case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		default:
			return fmt.Errorf("unknown expected last role %q", e.LastRole)
		}
	}
	if (e.LastContent != "" || e.LastToolID != "") && e.LastRole == "" {
		return fmt.Errorf("last_role is required when checking last_content or last_tool_id")
	}
	if e.LastToolID != "" && e.LastRole != RoleTool {
		return fmt.Errorf("last_tool_id requires last_role %q", RoleTool)
	}
	return nil
}

func (e scriptedExpectation) check(request *LLMRequest) error {
	if e.LastRole == "" {
		return nil
	}
	if request == nil || len(request.Messages) == 0 {
		return fmt.Errorf("expected a final %s message", e.LastRole)
	}
	last := request.Messages[len(request.Messages)-1]
	if last.Role != e.LastRole {
		return fmt.Errorf("last message role is %q, want %q", last.Role, e.LastRole)
	}
	if e.LastContent != "" && last.Content != e.LastContent {
		return fmt.Errorf("last message content does not match fixture")
	}
	if e.LastToolID != "" && last.ToolID != e.LastToolID {
		return fmt.Errorf("last message tool ID is %q, want %q", last.ToolID, e.LastToolID)
	}
	return nil
}
