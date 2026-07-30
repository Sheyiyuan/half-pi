package executor

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectDisplayArgsAppliesSchemaCentralAndNestedRules(t *testing.T) {
	tool := Tool{Parameters: &ObjectSchema{Properties: []PropertySchema{
		{Name: "visible", Type: "string", Display: DisplayShow},
		{Name: "masked", Type: "string", Display: DisplayMask},
		{Name: "hidden", Type: "string", Display: DisplayHide},
		{Name: "large", Type: "string", Display: DisplayPreview},
		{Name: "api_token", Type: "string", Display: DisplayShow},
		{Name: "nested", Type: "object", Display: DisplayShow},
	}}}
	view, err := ProjectDisplayArgs(tool, json.RawMessage(`{
		"visible":"hello","masked":"secret","hidden":"gone","large":"abcdefghijklmnopqrstuvwxyz",
		"api_token":"central-secret","note":"password=visible-warning",
		"nested":{"password":"nested-secret","value":"kept"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]DisplayExposure{
		"visible": DisplayShow, "masked": DisplayMask, "hidden": DisplayHide,
		"large": DisplayPreview, "api_token": DisplayMask,
		"note": DisplayShow, "nested.password": DisplayMask, "nested.value": DisplayShow,
	}
	for name, want := range checks {
		if got := view.Fields[name].State; got != want {
			t.Errorf("field %q state = %q, want %q", name, got, want)
		}
	}
	if view.Fields["hidden"].Value != nil || view.Fields["masked"].Value != "[masked]" {
		t.Fatalf("hide/mask values = %#v %#v", view.Fields["hidden"], view.Fields["masked"])
	}
	if len(view.Warnings) < 3 {
		t.Fatalf("warnings = %v", view.Warnings)
	}
}

func TestProjectDisplayArgsBoundsVisibleValuesDeterministically(t *testing.T) {
	args := json.RawMessage(`{"first":"` + strings.Repeat("a", MaxDisplayArgsBytes) + `","second":"tail"}`)
	first, err := ProjectDisplayArgs(Tool{}, args)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProjectDisplayArgs(Tool{}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Truncated || first.Fields["second"].State != DisplayPreview || first.Fields["second"].Preview != "" ||
		first.Fields["first"] != second.Fields["first"] || first.Fields["second"] != second.Fields["second"] {
		t.Fatalf("bounded projections differ: first=%+v second=%+v", first, second)
	}
}

func TestProjectDisplayOutputIsBoundedUTF8AndDigestRetainsOriginalLength(t *testing.T) {
	stdout := strings.Repeat("界", MaxDisplayOutputBytes/3+2)
	view := ProjectDisplayOutput(stdout, "stderr")
	if !view.Truncated || len(view.Stdout)+len(view.Stderr) > MaxDisplayOutputBytes || !utf8.ValidString(view.Stdout) {
		t.Fatalf("invalid truncation: bytes=%d truncated=%t utf8=%t", len(view.Stdout)+len(view.Stderr), view.Truncated, utf8.ValidString(view.Stdout))
	}
	if view.StdoutBytes != len(stdout) || view.StderrBytes != len("stderr") || !strings.HasPrefix(view.Digest, "sha256:") {
		t.Fatalf("metadata = %+v", view)
	}
}

func TestProjectDisplayOutputRepairsInvalidUTF8(t *testing.T) {
	view := ProjectDisplayOutput(string([]byte{'a', 0xff, 'b'}), "")
	if !utf8.ValidString(view.Stdout) || view.StdoutBytes != 3 || view.OutputBytes != 3 {
		t.Fatalf("invalid UTF-8 output projection = %+v", view)
	}
}
