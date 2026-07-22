package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPProvidersBoundSuccessfulResponseBeforeDecode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, strings.Repeat("x", 65))
	}))
	defer server.Close()

	providers := map[string]Provider{
		"openai":    NewOpenAI(server.URL, "secret", "model"),
		"gemini":    NewGemini(server.URL, "secret", "model"),
		"anthropic": NewAnthropic(server.URL, "secret", "model"),
	}
	for name, provider := range providers {
		t.Run(name, func(t *testing.T) {
			_, err := provider.Chat(context.Background(), &LLMRequest{ResponseByteLimit: 64})
			if !errors.Is(err, ErrResponseByteLimit) {
				t.Fatalf("Chat error = %v", err)
			}
		})
	}
}

func TestProviderErrorIsTypedAndDoesNotExposeBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "12")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, "token=highly-sensitive")
	}))
	defer server.Close()

	_, err := NewOpenAI(server.URL, "secret", "model").Chat(context.Background(), &LLMRequest{})
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("Chat error = %T %v", err, err)
	}
	if providerErr.Category != ProviderErrorRateLimited || providerErr.StatusCode != http.StatusTooManyRequests || providerErr.RetryAfter != 12*time.Second {
		t.Fatalf("ProviderError = %+v", providerErr)
	}
	if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "token=") {
		t.Fatalf("ProviderError exposed response body: %v", err)
	}
}

func TestParseRetryAfterRejectsExpiredAndInvalidValues(t *testing.T) {
	now := time.Date(2026, 7, 22, 1, 2, 3, 0, time.UTC)
	if got := parseRetryAfter("0", now); got != 0 {
		t.Fatalf("zero Retry-After = %v", got)
	}
	if got := parseRetryAfter("not-a-delay", now); got != 0 {
		t.Fatalf("invalid Retry-After = %v", got)
	}
	if got := parseRetryAfter(now.Add(-time.Second).Format(http.TimeFormat), now); got != 0 {
		t.Fatalf("expired Retry-After = %v", got)
	}
	if got := parseRetryAfter(now.Add(15*time.Second).Format(http.TimeFormat), now); got != 15*time.Second {
		t.Fatalf("date Retry-After = %v", got)
	}
}
