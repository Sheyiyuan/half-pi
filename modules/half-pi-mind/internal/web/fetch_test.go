package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFetchExtractsReadableHTML(t *testing.T) {
	client := newMemoryClient(func(request *http.Request) (*http.Response, error) {
		return memoryResponse(request, http.StatusOK, "text/html; charset=utf-8", `<!doctype html>
<html><head><title>Example &amp; Test</title><script>secret()</script></head>
<body><nav>Navigation</nav><main><h1>Hello</h1><p>First <strong>paragraph</strong>.</p><script>ignore me</script></main></body></html>`), nil
	})

	page, err := client.Fetch(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Example & Test" || !strings.Contains(page.Content, "Hello") || !strings.Contains(page.Content, "First paragraph.") {
		t.Fatalf("page = %+v", page)
	}
	if strings.Contains(page.Content, "Navigation") || strings.Contains(page.Content, "ignore me") || strings.Contains(page.Content, "secret") {
		t.Fatalf("page contains ignored content: %q", page.Content)
	}
}

func TestFetchTruncatesAtUTF8Boundary(t *testing.T) {
	client := newMemoryClient(func(request *http.Request) (*http.Response, error) {
		return memoryResponse(request, http.StatusOK, "text/plain; charset=utf-8", "你好世界"), nil
	})
	client.maxContentBytes = 7
	page, err := client.Fetch(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if !page.Truncated || !utf8.ValidString(page.Content) || page.Content != "你好" {
		t.Fatalf("page = %+v", page)
	}
}

func TestFetchRejectsBinaryContent(t *testing.T) {
	client := newMemoryClient(func(request *http.Request) (*http.Response, error) {
		return memoryResponse(request, http.StatusOK, "application/octet-stream", "\x00\x01\x02\x03"), nil
	})
	_, err := client.Fetch(context.Background(), "https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "unsupported content type") {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeInlineTextRemovesControls(t *testing.T) {
	if got := normalizeInlineText("Title\x00 with\n control !"); got != "Title with control!" {
		t.Fatalf("normalized text = %q", got)
	}
}
