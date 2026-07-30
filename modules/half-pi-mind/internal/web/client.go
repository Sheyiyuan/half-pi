// Package web 提供受限网页请求、正文提取和网页搜索能力。
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
)

const (
	defaultMaxResponseBytes int64 = 2 << 20
	defaultMaxContentBytes        = 50 << 10
	defaultRequestTimeout         = 15 * time.Second
	maxRedirects                  = 5
	maxURLBytes                   = 4096
)

type resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type clientOptions struct {
	allowPrivate     bool
	allowAnyPort     bool
	maxResponseBytes int64
	maxContentBytes  int
	resolver         resolver
}

// Client 是带请求边界和公网目标检查的网页客户端。
type Client struct {
	httpClient       *http.Client
	allowPrivate     bool
	allowAnyPort     bool
	maxResponseBytes int64
	maxContentBytes  int
}

type rawResponse struct {
	URL         string
	StatusCode  int
	ContentType string
	Body        []byte
}

// NewClient 创建使用安全默认值的网页客户端。
func NewClient() *Client {
	return newClient(clientOptions{})
}

var processClient = NewClient()

// DefaultClient 返回进程共享的网页客户端。
func DefaultClient() *Client {
	return processClient
}

// ValidateURL 检查 URL 是否符合公开网页请求的静态边界。
func ValidateURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}
	return validateRequestURL(parsed, false, false)
}

func newClient(options clientOptions) *Client {
	if options.maxResponseBytes <= 0 {
		options.maxResponseBytes = defaultMaxResponseBytes
	}
	if options.maxContentBytes <= 0 {
		options.maxContentBytes = defaultMaxContentBytes
	}
	if options.resolver == nil {
		options.resolver = net.DefaultResolver
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialChecked(ctx, network, address, options.resolver, dialer, options.allowPrivate)
		},
	}

	client := &Client{
		allowPrivate:     options.allowPrivate,
		allowAnyPort:     options.allowAnyPort,
		maxResponseBytes: options.maxResponseBytes,
		maxContentBytes:  options.maxContentBytes,
	}
	client.httpClient = &http.Client{
		Transport: transport,
		Timeout:   defaultRequestTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return validateRequestURL(request.URL, options.allowPrivate, options.allowAnyPort)
		},
	}
	return client
}

func (c *Client) get(ctx context.Context, rawURL string) (*rawResponse, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	if err := validateRequestURL(parsed, c.allowPrivate, c.allowAnyPort); err != nil {
		return nil, err
	}
	parsed.Fragment = ""

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			if err := waitForRetry(ctx, 350*time.Millisecond); err != nil {
				return nil, err
			}
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		setBrowserHeaders(request.Header)

		response, err := c.httpClient.Do(request)
		if err != nil {
			lastErr = fmt.Errorf("request URL: %w", err)
			if !isRetryableError(err) {
				return nil, lastErr
			}
			continue
		}

		body, readErr := readBounded(response.Body, c.maxResponseBytes)
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close response: %w", closeErr)
		}
		if isRetryableStatus(response.StatusCode) && attempt == 0 {
			lastErr = fmt.Errorf("HTTP status %d", response.StatusCode)
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("HTTP status %d", response.StatusCode)
		}
		return &rawResponse{
			URL:         response.Request.URL.String(),
			StatusCode:  response.StatusCode,
			ContentType: response.Header.Get("Content-Type"),
			Body:        body,
		}, nil
	}
	return nil, lastErr
}

func setBrowserHeaders(header http.Header) {
	header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36")
	header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain;q=0.9,*/*;q=0.5")
	header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	header.Set("Cache-Control", "no-cache")
}

func validateRequestURL(target *url.URL, allowPrivate, allowAnyPort bool) error {
	if target == nil || len(target.String()) > maxURLBytes {
		return fmt.Errorf("URL is empty or too long")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are supported")
	}
	if target.Hostname() == "" {
		return fmt.Errorf("URL host is required")
	}
	if target.User != nil {
		return fmt.Errorf("URL credentials are not allowed")
	}
	port := target.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("invalid URL port")
		}
		if !allowAnyPort && port != "80" && port != "443" {
			return fmt.Errorf("only ports 80 and 443 are supported")
		}
	}

	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	if !allowPrivate && security.IsLocalNetworkHostname(host) {
		return fmt.Errorf("local network hosts are not allowed")
	}
	if address, err := netip.ParseAddr(host); err == nil && !allowPrivate && !security.IsPublicNetworkAddress(address) {
		return fmt.Errorf("private or non-routable addresses are not allowed")
	}
	return nil
}

func dialChecked(ctx context.Context, network, address string, lookup resolver, dialer *net.Dialer, allowPrivate bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split target address: %w", err)
	}

	var addresses []net.IPAddr
	if parsed, parseErr := netip.ParseAddr(strings.Trim(host, "[]")); parseErr == nil {
		addresses = []net.IPAddr{{IP: net.IP(parsed.AsSlice()), Zone: parsed.Zone()}}
	} else {
		addresses, err = lookup.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve host %s: %w", host, err)
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("host %s has no addresses", host)
	}

	for _, candidate := range addresses {
		parsed, ok := netip.AddrFromSlice(candidate.IP)
		if !ok || (!allowPrivate && !security.IsPublicNetworkAddress(parsed)) {
			return nil, fmt.Errorf("host %s resolves to a private or non-routable address", host)
		}
	}

	var errs []error
	for _, candidate := range addresses {
		candidateHost := candidate.IP.String()
		if candidate.Zone != "" {
			candidateHost += "%" + candidate.Zone
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidateHost, port))
		if dialErr == nil {
			return connection, nil
		}
		errs = append(errs, dialErr)
	}
	return nil, fmt.Errorf("dial host %s: %w", host, errors.Join(errs...))
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func isRetryableError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
