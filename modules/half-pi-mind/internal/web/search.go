package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	bingSearchURL       = "https://www.bing.com/search"
	duckDuckGoSearchURL = "https://html.duckduckgo.com/html/"
	searchTimeout       = 8 * time.Second
)

// SearchResult 是一条网页搜索结果。
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// Search 使用无需密钥的公开 HTML 搜索端点搜索网页。
func (c *Client) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}
	if count <= 0 {
		count = 5
	}
	if count > 10 {
		count = 10
	}
	type provider struct {
		name   string
		target string
		parse  func([]byte, string, int) ([]SearchResult, error)
	}
	providers := []provider{
		{name: "bing", target: bingSearchTarget(query, count), parse: parseBingResults},
		{name: "duckduckgo", target: duckDuckGoSearchURL + "?q=" + url.QueryEscape(query), parse: parseDuckDuckGoResults},
	}

	var providerErrors []error
	hadSuccessfulResponse := false
	for _, candidate := range providers {
		requestCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		response, err := c.get(requestCtx, candidate.target)
		cancel()
		if err != nil {
			providerErrors = append(providerErrors, fmt.Errorf("%s: %w", candidate.name, err))
			continue
		}
		results, err := candidate.parse(response.Body, response.ContentType, count)
		if err != nil {
			providerErrors = append(providerErrors, fmt.Errorf("%s: parse results: %w", candidate.name, err))
			continue
		}
		hadSuccessfulResponse = true
		if len(results) > 0 {
			return results, nil
		}
	}
	if hadSuccessfulResponse {
		return []SearchResult{}, nil
	}
	return nil, fmt.Errorf("all search providers failed: %w", errors.Join(providerErrors...))
}

func bingSearchTarget(query string, count int) string {
	values := url.Values{}
	values.Set("q", query)
	values.Set("count", fmt.Sprintf("%d", count))
	values.Set("setlang", "zh-Hans")
	return bingSearchURL + "?" + values.Encode()
}

func parseBingResults(data []byte, contentType string, count int) ([]SearchResult, error) {
	document, err := parseHTMLDocument(data, contentType)
	if err != nil {
		return nil, err
	}
	containers := nodesWithClass(document, "b_algo")
	results := make([]SearchResult, 0, min(count, len(containers)))
	seen := make(map[string]struct{})
	for _, container := range containers {
		heading := findElement(container, "h2")
		link := findElement(heading, "a")
		resolved := resolveResultURL(attribute(link, "href"), bingSearchURL)
		title := strings.TrimSpace(nodeText(link))
		if title == "" || resolved == "" {
			continue
		}
		if _, exists := seen[resolved]; exists {
			continue
		}
		seen[resolved] = struct{}{}
		caption := firstNodeWithClass(container, "b_caption")
		snippet := strings.TrimSpace(nodeText(findElement(caption, "p")))
		results = append(results, SearchResult{Title: title, URL: resolved, Snippet: snippet})
		if len(results) >= count {
			break
		}
	}
	return results, nil
}

func parseDuckDuckGoResults(data []byte, contentType string, count int) ([]SearchResult, error) {
	document, err := parseHTMLDocument(data, contentType)
	if err != nil {
		return nil, err
	}

	containers := nodesWithClass(document, "result")
	results := make([]SearchResult, 0, min(count, len(containers)))
	seen := make(map[string]struct{})
	for _, container := range containers {
		link := firstNodeWithClass(container, "result__a")
		if link == nil {
			continue
		}
		resolved := resolveDuckDuckGoURL(attribute(link, "href"))
		title := strings.TrimSpace(nodeText(link))
		if title == "" || resolved == "" {
			continue
		}
		if _, exists := seen[resolved]; exists {
			continue
		}
		seen[resolved] = struct{}{}
		snippet := strings.TrimSpace(nodeText(firstNodeWithClass(container, "result__snippet")))
		results = append(results, SearchResult{Title: title, URL: resolved, Snippet: snippet})
		if len(results) >= count {
			break
		}
	}
	return results, nil
}

func parseHTMLDocument(data []byte, contentType string) (*html.Node, error) {
	reader, err := charset.NewReader(bytes.NewReader(data), contentType)
	if err != nil {
		reader = bytes.NewReader(data)
	}
	document, err := html.Parse(reader)
	if err != nil {
		return nil, err
	}
	return document, nil
}

func nodesWithClass(root *html.Node, class string) []*html.Node {
	var matches []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && hasClass(node, class) {
			matches = append(matches, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return matches
}

func firstNodeWithClass(root *html.Node, class string) *html.Node {
	if root == nil {
		return nil
	}
	if root.Type == html.ElementNode && hasClass(root, class) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := firstNodeWithClass(child, class); found != nil {
			return found
		}
	}
	return nil
}

func hasClass(node *html.Node, expected string) bool {
	for _, attr := range node.Attr {
		if attr.Key != "class" {
			continue
		}
		for _, class := range strings.Fields(attr.Val) {
			if class == expected {
				return true
			}
		}
	}
	return false
}

func attribute(node *html.Node, name string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func resolveDuckDuckGoURL(href string) string {
	resolved := resolveResultURL(href, duckDuckGoSearchURL)
	if resolved == "" {
		return ""
	}
	parsed, err := url.Parse(resolved)
	if err != nil {
		return ""
	}
	if redirected := parsed.Query().Get("uddg"); redirected != "" {
		return resolveResultURL(redirected, duckDuckGoSearchURL)
	}
	return resolved
}

func resolveResultURL(href, baseURL string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	base, _ := url.Parse(baseURL)
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	parsed = base.ResolveReference(parsed)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	parsed.Fragment = ""
	return parsed.String()
}
