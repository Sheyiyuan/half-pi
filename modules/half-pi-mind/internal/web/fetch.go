package web

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// FetchResult 是网页抓取后的可读内容。
type FetchResult struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	StatusCode  int    `json:"status_code"`
	Truncated   bool   `json:"truncated"`
}

// Fetch 获取一个公开 HTTP(S) 页面并提取文本内容。
func (c *Client) Fetch(ctx context.Context, target string) (*FetchResult, error) {
	response, err := c.get(ctx, target)
	if err != nil {
		return nil, err
	}

	contentType := response.ContentType
	mediaType, _, parseErr := mime.ParseMediaType(contentType)
	if parseErr != nil || mediaType == "" {
		mediaType = http.DetectContentType(response.Body)
		if detected, _, err := mime.ParseMediaType(mediaType); err == nil {
			mediaType = detected
		}
	}

	var title, content string
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		title, content, err = extractHTML(response.Body, contentType)
		if err != nil {
			return nil, fmt.Errorf("extract HTML: %w", err)
		}
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/xml":
		content = strings.ToValidUTF8(string(response.Body), "\uFFFD")
	default:
		return nil, fmt.Errorf("unsupported content type %q", mediaType)
	}

	content = strings.TrimSpace(content)
	content, truncated := truncateUTF8(content, c.maxContentBytes)
	return &FetchResult{
		URL:         response.URL,
		Title:       title,
		ContentType: mediaType,
		Content:     content,
		StatusCode:  response.StatusCode,
		Truncated:   truncated,
	}, nil
}

func extractHTML(data []byte, contentType string) (string, string, error) {
	reader, err := charset.NewReader(bytes.NewReader(data), contentType)
	if err != nil {
		reader = bytes.NewReader(data)
	}
	document, err := html.Parse(reader)
	if err != nil {
		return "", "", err
	}
	title := strings.TrimSpace(nodeText(findElement(document, "title")))
	root := findElement(document, "article")
	if root == nil {
		root = findElement(document, "main")
	}
	if root == nil {
		root = findElement(document, "body")
	}
	if root == nil {
		root = document
	}
	return title, visibleText(root), nil
}

func findElement(node *html.Node, name string) *html.Node {
	if node == nil {
		return nil
	}
	if node.Type == html.ElementNode && node.Data == name {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func visibleText(root *html.Node) string {
	var output strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && ignoredElement(node.Data) {
			return
		}
		if node.Type == html.TextNode {
			text := strings.Join(strings.Fields(node.Data), " ")
			if text != "" {
				output.WriteString(text)
				output.WriteByte(' ')
			}
			return
		}
		if node.Type == html.ElementNode && node.Data == "br" {
			output.WriteByte('\n')
		}
		block := node.Type == html.ElementNode && blockElement(node.Data)
		if block {
			output.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if block {
			output.WriteByte('\n')
		}
	}
	walk(root)
	return normalizeLines(output.String())
}

func nodeText(root *html.Node) string {
	if root == nil {
		return ""
	}
	var output strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			output.WriteString(node.Data)
			output.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return normalizeInlineText(output.String())
}

func normalizeInlineText(content string) string {
	content = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, content)
	return tightenPunctuation(strings.Join(strings.Fields(content), " "))
}

func ignoredElement(name string) bool {
	switch name {
	case "script", "style", "noscript", "svg", "canvas", "template", "form", "nav", "header", "footer", "aside":
		return true
	default:
		return false
	}
}

func blockElement(name string) bool {
	switch name {
	case "address", "article", "blockquote", "div", "dl", "fieldset", "figure", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "li", "main", "ol", "p", "pre", "section", "table", "tr", "ul":
		return true
	default:
		return false
	}
}

func normalizeLines(content string) string {
	lines := strings.Split(content, "\n")
	cleaned := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\t' {
				return -1
			}
			return r
		}, line)
		line = tightenPunctuation(strings.Join(strings.Fields(line), " "))
		if line == "" {
			if len(cleaned) > 0 && !blank {
				cleaned = append(cleaned, "")
				blank = true
			}
			continue
		}
		cleaned = append(cleaned, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func tightenPunctuation(line string) string {
	replacer := strings.NewReplacer(
		" .", ".", " ,", ",", " ;", ";", " :", ":", " !", "!", " ?", "?",
		" )", ")", " ]", "]", " }", "}", "( ", "(", "[ ", "[", "{ ", "{",
		" 。", "。", " ，", "，", " ；", "；", " ：", "：", " ！", "！", " ？", "？",
	)
	return replacer.Replace(line)
}

func truncateUTF8(content string, limit int) (string, bool) {
	if len(content) <= limit {
		return content, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(content[end]) {
		end--
	}
	return strings.TrimSpace(content[:end]), true
}
