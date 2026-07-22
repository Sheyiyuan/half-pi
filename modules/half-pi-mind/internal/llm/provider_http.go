package llm

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProviderErrorCategory 是不携带 provider 正文的稳定 HTTP 错误分类。
type ProviderErrorCategory string

const (
	ProviderErrorHTTP        ProviderErrorCategory = "http_error"
	ProviderErrorRateLimited ProviderErrorCategory = "rate_limited"
)

// ErrResponseByteLimit 表示成功响应在 JSON 解码前超过本地字节上限。
var ErrResponseByteLimit = errors.New("provider response body exceeded limit")

// ProviderError 描述 provider HTTP 失败，不保存或暴露响应正文。
type ProviderError struct {
	Category      ProviderErrorCategory
	StatusCode    int
	RetryAfter    time.Duration
	BodyTruncated bool
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider HTTP error"
	}
	message := fmt.Sprintf("provider HTTP status %d", e.StatusCode)
	if e.Category == ProviderErrorRateLimited {
		message += " (rate limited)"
	}
	if e.BodyTruncated {
		message += " (response body exceeded limit)"
	}
	return message
}

func readHTTPResponse(resp *http.Response, successLimit int64) ([]byte, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		truncated := discardBoundedProviderBody(resp.Body)
		category := ProviderErrorHTTP
		if resp.StatusCode == http.StatusTooManyRequests {
			category = ProviderErrorRateLimited
		}
		return nil, &ProviderError{
			Category: category, StatusCode: resp.StatusCode,
			RetryAfter:    parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
			BodyTruncated: truncated,
		}
	}
	if successLimit <= 0 {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		return data, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, successLimit+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if int64(len(data)) > successLimit {
		return nil, ErrResponseByteLimit
	}
	return data, nil
}

func discardBoundedProviderBody(body io.Reader) bool {
	data, err := io.ReadAll(io.LimitReader(body, maxProviderErrorBody+1))
	return err == nil && len(data) > maxProviderErrorBody
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 || seconds > int64((time.Duration(1<<63-1))/time.Second) {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	delay := when.Sub(now)
	if delay <= 0 {
		return 0
	}
	return delay
}
