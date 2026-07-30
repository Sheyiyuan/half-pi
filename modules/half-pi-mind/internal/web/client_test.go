package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

type staticResolver []net.IPAddr

func (addresses staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return addresses, nil
}

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newMemoryClient(fn roundTripFunc) *Client {
	return &Client{
		httpClient:       &http.Client{Transport: fn},
		allowPrivate:     false,
		allowAnyPort:     false,
		maxResponseBytes: defaultMaxResponseBytes,
		maxContentBytes:  defaultMaxContentBytes,
	}
}

func memoryResponse(request *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestClientRetriesTransientStatus(t *testing.T) {
	var calls atomic.Int32
	client := newMemoryClient(func(request *http.Request) (*http.Response, error) {
		if !strings.Contains(request.UserAgent(), "Mozilla/5.0") {
			t.Errorf("user agent = %q", request.UserAgent())
		}
		if calls.Add(1) == 1 {
			return memoryResponse(request, http.StatusServiceUnavailable, "text/plain", "wait"), nil
		}
		return memoryResponse(request, http.StatusOK, "text/plain", "ready"), nil
	})

	page, err := client.Fetch(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if page.Content != "ready" || calls.Load() != 2 {
		t.Fatalf("page = %+v, calls = %d", page, calls.Load())
	}
}

func TestClientRejectsPrivateAddress(t *testing.T) {
	_, err := NewClient().Fetch(context.Background(), "http://127.0.0.1/")
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("error = %v, want private address rejection", err)
	}
}

func TestClientLimitsResponseBody(t *testing.T) {
	client := newMemoryClient(func(request *http.Request) (*http.Response, error) {
		return memoryResponse(request, http.StatusOK, "text/plain", "response is too large"), nil
	})
	client.maxResponseBytes = 8
	_, err := client.Fetch(context.Background(), "https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "exceeds 8 bytes") {
		t.Fatalf("error = %v, want response limit", err)
	}
}

func TestValidateRequestURLRejectsCredentialsAndPorts(t *testing.T) {
	for _, target := range []string{
		"https://user:pass@example.com/",
		"https://example.com:8443/",
		"file:///tmp/secret",
	} {
		request, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRequestURL(request.URL, false, false); err == nil {
			t.Fatalf("target %q was allowed", target)
		}
	}
}

func TestDialCheckedRejectsMixedPublicAndPrivateDNS(t *testing.T) {
	lookup := staticResolver{
		{IP: net.ParseIP("8.8.8.8")},
		{IP: net.ParseIP("127.0.0.1")},
	}
	_, err := dialChecked(context.Background(), "tcp", "example.com:443", lookup, &net.Dialer{}, false)
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("error = %v, want mixed DNS rejection", err)
	}
}
