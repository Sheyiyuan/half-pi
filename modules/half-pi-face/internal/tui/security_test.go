package tui

import (
	"strings"
	"testing"
)

func TestSanitizeRemoteTextRemovesTerminalControls(t *testing.T) {
	input := "before\x1b[31mred\x1b[0m\r\x00\u009bafter\n中文\ttext"
	got := sanitizeRemoteText(input)
	if strings.ContainsAny(got, "\x1b\r\x00\u009b") || got != "beforeredafter\n中文\ttext" {
		t.Fatalf("sanitized text = %q", got)
	}
}

func TestRetryBackoffIsCappedAndJittered(t *testing.T) {
	if low, high := retryDelay(0, 0), retryDelay(0, 1); low != 800000000 || high != 1200000000 {
		t.Fatalf("first retry range = %s..%s", low, high)
	}
	if low, high := retryDelay(20, 0), retryDelay(20, 1); low != 24e9 || high != 36e9 {
		t.Fatalf("capped retry range = %s..%s", low, high)
	}
}
