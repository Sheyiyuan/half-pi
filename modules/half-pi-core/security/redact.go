package security

import (
	"regexp"
	"strings"
)

const redactedValue = "<redacted>"

var (
	sensitiveFieldPattern = regexp.MustCompile(`(?i)^(token|api[_-]?key|application[_-]?key|authorization|password|passwd|secret|cookie|private[_-]?key)$`)
	privateKeyPattern     = regexp.MustCompile(`(?is)-----BEGIN(?: [A-Z0-9]+)? PRIVATE KEY-----.*?-----END(?: [A-Z0-9]+)? PRIVATE KEY-----`)
	authorizationPattern  = regexp.MustCompile(`(?i)\b(bearer|basic)([ \t]+)[A-Za-z0-9+/=_:.-]{8,}`)
	assignmentPattern     = regexp.MustCompile(`(?i)(["']?(?:token|api[_-]?key|application[_-]?key|authorization|password|passwd|secret|cookie|private[_-]?key)["']?[ \t]*[:=][ \t]*)(["']?)[^\s,"';}]+(["']?)`)
	credentialPattern     = regexp.MustCompile(`\b(?:sk|pk|ghp|github_pat|xox[baprs])[-_][A-Za-z0-9_-]{16,}\b`)
)

// SecretScan 是文本秘密扫描与确定性脱敏结果。
type SecretScan struct {
	Text   string
	Found  bool
	Unsafe bool
}

// IsSensitiveFieldName 判断结构化字段名是否属于禁止自由传递的秘密类别。
func IsSensitiveFieldName(name string) bool {
	return sensitiveFieldPattern.MatchString(strings.TrimSpace(name))
}

// RedactSensitiveText 脱敏常见凭据；私钥块标记为 Unsafe，调用方应拒绝整个输入。
func RedactSensitiveText(text string) SecretScan {
	result := SecretScan{Text: text}
	if privateKeyPattern.MatchString(result.Text) {
		result.Found, result.Unsafe = true, true
		result.Text = privateKeyPattern.ReplaceAllString(result.Text, redactedValue)
	}
	if authorizationPattern.MatchString(result.Text) {
		result.Found = true
		result.Text = authorizationPattern.ReplaceAllString(result.Text, `${1}${2}`+redactedValue)
	}
	if assignmentPattern.MatchString(result.Text) {
		result.Found = true
		result.Text = assignmentPattern.ReplaceAllString(result.Text, `${1}`+redactedValue)
	}
	if credentialPattern.MatchString(result.Text) {
		result.Found = true
		result.Text = credentialPattern.ReplaceAllString(result.Text, redactedValue)
	}
	return result
}
