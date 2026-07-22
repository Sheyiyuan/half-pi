package compact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

// TokenEstimator 是 Compact 全路径共用的确定性保守估算器。
type TokenEstimator struct{}

// RequestFingerprint 证明 system、工具和消息前缀的规范身份。
type RequestFingerprint struct {
	SystemDigest   string
	ToolDefsDigest string
	MessageDigest  string
	MessageCount   int
}

// UsageAnchor 保存同 provider/model 上一次有效 input usage 的内存锚点。
type UsageAnchor struct {
	ProviderID  string
	ModelID     string
	Fingerprint RequestFingerprint
	InputTokens int64
}

// EstimateText 按 CJK、代码块、符号和 Unicode 感知规则估算文本。
func (TokenEstimator) EstimateText(text string) int64 {
	if text == "" {
		return 0
	}
	runes := []rune(text)
	var asciiAlnum, punctuation, lineBreaks, whitespace, otherBytes int64
	var codeNonWhitespace, codeWhitespace int64
	inFence := false
	for index := 0; index < len(runes); {
		if index+2 < len(runes) && runes[index] == '`' && runes[index+1] == '`' && runes[index+2] == '`' {
			if inFence {
				codeNonWhitespace += 3
			} else {
				punctuation += 3
			}
			inFence = !inFence
			index += 3
			continue
		}
		r := runes[index]
		if r == '\r' || r == '\n' {
			lineBreaks++
			if r == '\r' && index+1 < len(runes) && runes[index+1] == '\n' {
				index++
			}
			index++
			continue
		}
		if inFence {
			if unicode.IsSpace(r) {
				codeWhitespace++
			} else {
				codeNonWhitespace++
			}
			index++
			continue
		}
		switch {
		case isCJK(r):
			punctuation++
		case r < utf8.RuneSelf && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')):
			asciiAlnum++
		case unicode.IsSpace(r):
			whitespace++
		case r < utf8.RuneSelf:
			punctuation++
		default:
			otherBytes += int64(utf8.RuneLen(r))
		}
		index++
	}
	return ceilDiv(asciiAlnum, 3) + punctuation + lineBreaks + ceilDiv(whitespace, 4) +
		ceilDiv(otherBytes*2, 3) + codeNonWhitespace + ceilDiv(codeWhitespace, 4)
}

// EstimateMessage 估算单条 provider-visible 消息及固定 framing。
func (e TokenEstimator) EstimateMessage(message llm.Message) int64 {
	tokens := e.EstimateText(message.Content) + e.EstimateText(string(message.Role)) + e.EstimateText(message.ToolID) + 12
	if len(message.ToolCalls) > 0 {
		encoded, _ := json.Marshal(message.ToolCalls)
		tokens += e.EstimateText(string(encoded))
	}
	return tokens
}

// EstimateRequest 对完整请求应用一次 10% safety margin。
func (e TokenEstimator) EstimateRequest(request llm.LLMRequest) int64 {
	base := e.EstimateText(request.System)
	for _, message := range request.Messages {
		base += e.EstimateMessage(message)
	}
	for _, tool := range request.Tools {
		encoded, _ := json.Marshal(tool)
		base += e.EstimateText(string(encoded)) + 16
	}
	return ceilDiv(base*110, 100)
}

// Fingerprint 计算 provider-visible 请求的固定长度身份。
func (TokenEstimator) Fingerprint(request llm.LLMRequest) RequestFingerprint {
	system := digestBytes([]byte(request.System))
	tools, _ := json.Marshal(request.Tools)
	messageHash := sha256.New()
	writeDigestField(messageHash, "half-pi:compact-request-messages:v1")
	for _, message := range request.Messages {
		writeMessageDigest(messageHash, message)
	}
	return RequestFingerprint{
		SystemDigest: system, ToolDefsDigest: digestBytes(tools),
		MessageDigest: "sha256:" + hex.EncodeToString(messageHash.Sum(nil)), MessageCount: len(request.Messages),
	}
}

// EstimateWithAnchor 在严格前缀匹配时使用 provider usage，否则回退完整估算。
func (e TokenEstimator) EstimateWithAnchor(request llm.LLMRequest, providerID, modelID string, anchor *UsageAnchor) (int64, bool) {
	if anchor == nil || anchor.InputTokens <= 0 || anchor.ProviderID != providerID || anchor.ModelID != modelID ||
		anchor.Fingerprint.MessageCount > len(request.Messages) {
		return e.EstimateRequest(request), false
	}
	current := e.Fingerprint(llm.LLMRequest{System: request.System, Tools: request.Tools, Messages: request.Messages[:anchor.Fingerprint.MessageCount]})
	if current != anchor.Fingerprint {
		return e.EstimateRequest(request), false
	}
	var delta int64
	for _, message := range request.Messages[anchor.Fingerprint.MessageCount:] {
		delta += e.EstimateMessage(message)
	}
	return anchor.InputTokens + ceilDiv(delta*110, 100), true
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func ceilDiv(value, divisor int64) int64 {
	if value <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func writeMessageDigest(writer interface{ Write([]byte) (int, error) }, message llm.Message) {
	writeDigestField(writer, string(message.Role))
	writeDigestField(writer, message.Content)
	writeDigestField(writer, message.ToolID)
	toolCalls, _ := json.Marshal(message.ToolCalls)
	writeDigestField(writer, string(toolCalls))
}

func writeDigestField(writer interface{ Write([]byte) (int, error) }, value string) {
	_, _ = fmt.Fprintf(writer, "%016x", len(value))
	_, _ = writer.Write([]byte(value))
}
