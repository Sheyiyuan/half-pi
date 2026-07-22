package repl

import (
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
)

func TestParseCompactTarget(t *testing.T) {
	tests := []struct {
		input string
		check func(compact.Target) bool
	}{
		{"", func(target compact.Target) bool { _, ok := target.(compact.DefaultTarget); return ok }},
		{"60%", func(target compact.Target) bool {
			value, ok := target.(compact.RatioTarget)
			return ok && value.Ratio == .60
		}},
		{"keep 40", func(target compact.Target) bool {
			value, ok := target.(compact.KeepTarget)
			return ok && value.Messages == 40
		}},
		{"rebase", func(target compact.Target) bool { _, ok := target.(compact.RebaseTarget); return ok }},
	}
	for _, test := range tests {
		target, err := parseCompactTarget(test.input)
		if err != nil || !test.check(target) {
			t.Errorf("parse %q = %#v, %v", test.input, target, err)
		}
	}
	for _, input := range []string{"19%", "95%", "keep 0", "keep 10001", "status now", "default", "60% extra"} {
		if target, err := parseCompactTarget(input); err == nil {
			t.Errorf("invalid %q parsed as %#v", input, target)
		}
	}
}
