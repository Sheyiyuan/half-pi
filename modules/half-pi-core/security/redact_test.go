package security

import (
	"strings"
	"testing"
)

func TestRedactSensitiveText(t *testing.T) {
	original := `authorization: Bearer abcdefghijklmnop api_key="sk-abcdefghijklmnopqrstuvwxyz" password=hunter2`
	result := RedactSensitiveText(original)
	if !result.Found || result.Unsafe {
		t.Fatalf("scan = %+v", result)
	}
	for _, secret := range []string{"abcdefghijklmnop", "sk-abcdefghijklmnopqrstuvwxyz", "hunter2"} {
		if strings.Contains(result.Text, secret) {
			t.Fatalf("redacted text contains %q: %s", secret, result.Text)
		}
	}
	if strings.Count(result.Text, redactedValue) < 2 {
		t.Fatalf("redacted text = %q", result.Text)
	}
}

func TestRedactSensitiveTextMarksPrivateKeysUnsafe(t *testing.T) {
	result := RedactSensitiveText("-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----")
	if !result.Found || !result.Unsafe || strings.Contains(result.Text, "secret") {
		t.Fatalf("scan = %+v", result)
	}
}

func TestSensitiveFieldNamesAreClosed(t *testing.T) {
	for _, name := range []string{"token", "API_KEY", "application-key", "private_key", "password"} {
		if !IsSensitiveFieldName(name) {
			t.Fatalf("%q was not sensitive", name)
		}
	}
	for _, name := range []string{"status", "tool", "task_id"} {
		if IsSensitiveFieldName(name) {
			t.Fatalf("%q was unexpectedly sensitive", name)
		}
	}
}
