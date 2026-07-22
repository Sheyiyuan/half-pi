package compact

import (
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

func TestTokenEstimatorTextClasses(t *testing.T) {
	estimator := TokenEstimator{}
	tests := map[string]struct {
		text string
		want int64
	}{
		"ascii words": {text: "abcdef", want: 2},
		"CJK":         {text: "中文かな한", want: 5},
		"punctuation": {text: "{}[]!", want: 5},
		"CRLF":        {text: "a\r\nb", want: 2},
		"whitespace":  {text: "        ", want: 2},
		"emoji":       {text: "😀", want: 3},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := estimator.EstimateText(test.text); got != test.want {
				t.Fatalf("EstimateText(%q) = %d, want %d", test.text, got, test.want)
			}
		})
	}
}

func TestTokenEstimatorFencedCodeIsConservative(t *testing.T) {
	estimator := TokenEstimator{}
	plain := estimator.EstimateText("identifier anotherIdentifier")
	fenced := estimator.EstimateText("```go\nidentifier anotherIdentifier\n```")
	if plain >= fenced {
		t.Fatalf("plain=%d fenced=%d", plain, fenced)
	}
	if unclosed := estimator.EstimateText("```\nabc def"); unclosed <= estimator.EstimateText("abc def") {
		t.Fatalf("unclosed fence was not treated as code: %d", unclosed)
	}
}

func TestRequestFingerprintExcludesLocalMessageFields(t *testing.T) {
	estimator := TokenEstimator{}
	request := llm.LLMRequest{
		System: "system", Messages: []llm.Message{{
			Role: llm.RoleTool, Content: "result", ToolID: "call-1",
			RequestID: "request-a", CompactProjection: `{"private":true}`,
		}},
		Tools: []llm.ToolDef{{Name: "read_file", Description: "read", Parameters: map[string]any{"type": "object"}}},
	}
	changedLocal := request
	changedLocal.Messages = append([]llm.Message(nil), request.Messages...)
	changedLocal.Messages[0].RequestID = "request-b"
	changedLocal.Messages[0].CompactProjection = `{"other":true}`
	if estimator.Fingerprint(request) != estimator.Fingerprint(changedLocal) {
		t.Fatal("local-only fields changed provider fingerprint")
	}
	changedContent := changedLocal
	changedContent.Messages = append([]llm.Message(nil), changedLocal.Messages...)
	changedContent.Messages[0].Content = "different"
	if estimator.Fingerprint(request) == estimator.Fingerprint(changedContent) {
		t.Fatal("provider-visible content did not change fingerprint")
	}
}

func TestUsageAnchorOnlyAppliesToExactPrefix(t *testing.T) {
	estimator := TokenEstimator{}
	base := llm.LLMRequest{System: "system", Messages: []llm.Message{{Role: llm.RoleUser, Content: "one"}}}
	anchor := &UsageAnchor{
		ProviderID: "provider", ModelID: "model", Fingerprint: estimator.Fingerprint(base), InputTokens: 100,
	}
	appended := base
	appended.Messages = append(append([]llm.Message(nil), base.Messages...), llm.Message{Role: llm.RoleAssistant, Content: "two"})
	got, anchored := estimator.EstimateWithAnchor(appended, "provider", "model", anchor)
	want := int64(100) + ceilDiv(estimator.EstimateMessage(appended.Messages[1])*110, 100)
	if !anchored || got != want {
		t.Fatalf("anchored estimate=%d anchored=%t want=%d", got, anchored, want)
	}
	changed := appended
	changed.Messages = append([]llm.Message(nil), appended.Messages...)
	changed.Messages[0].Content = "changed"
	if _, anchored := estimator.EstimateWithAnchor(changed, "provider", "model", anchor); anchored {
		t.Fatal("changed prefix used provider usage anchor")
	}
	if _, anchored := estimator.EstimateWithAnchor(appended, "other", "model", anchor); anchored {
		t.Fatal("different provider used provider usage anchor")
	}
}
