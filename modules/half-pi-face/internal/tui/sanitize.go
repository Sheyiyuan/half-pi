package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func sanitizeRemoteText(value string) string {
	var result strings.Builder
	for index := 0; index < len(value); {
		if value[index] == 0x1b {
			index = skipEscapeSequence(value, index)
			continue
		}
		r, size := rune(value[index]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRuneInString(value[index:])
		}
		index += size
		if r == '\n' || r == '\t' {
			result.WriteRune(r)
			continue
		}
		if r == '\r' || unicode.IsControl(r) {
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func skipEscapeSequence(value string, index int) int {
	index++
	if index >= len(value) {
		return index
	}
	switch value[index] {
	case '[':
		index++
		for index < len(value) {
			current := value[index]
			index++
			if current >= 0x40 && current <= 0x7e {
				break
			}
		}
	case ']':
		index++
		for index < len(value) {
			if value[index] == 0x07 {
				return index + 1
			}
			if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
				return index + 2
			}
			index++
		}
	default:
		index++
	}
	return index
}

func sanitizeInput(value string) string {
	var result strings.Builder
	for _, r := range value {
		switch {
		case r == '\n' || r == '\t':
			result.WriteRune(r)
		case r == '\r':
			result.WriteRune('\n')
		case !unicode.IsControl(r):
			result.WriteRune(r)
		}
	}
	return result.String()
}

func truncateUTF8Bytes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	used := 0
	for index, r := range value {
		size := utf8.RuneLen(r)
		if used+size > limit {
			return value[:index]
		}
		used += size
	}
	return value
}
