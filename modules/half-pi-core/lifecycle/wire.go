package lifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// EncodeRedactedEvent 编码稳定的 schema v1 wire 视图。
// Sensitive 永不进入 wire，即使内存 Observer 拥有额外读取 capability。
func EncodeRedactedEvent(event RedactedEvent) ([]byte, error) {
	if err := ValidateRedactedEvent(event); err != nil {
		return nil, err
	}
	event.Sensitive = nil
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode lifecycle event: %w", err)
	}
	return encoded, nil
}

// DecodeRedactedEvent 严格解码 schema v1 wire 视图。
func DecodeRedactedEvent(encoded []byte) (RedactedEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var event RedactedEvent
	if err := decoder.Decode(&event); err != nil {
		return RedactedEvent{}, fmt.Errorf("decode lifecycle event: %w", err)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return RedactedEvent{}, fmt.Errorf("decode lifecycle event: trailing JSON")
	}
	if err := ValidateRedactedEvent(event); err != nil {
		return RedactedEvent{}, err
	}
	return event, nil
}

// ValidateRedactedEvent 校验可交给外部 consumer 的最小身份与阶段契约。
func ValidateRedactedEvent(event RedactedEvent) error {
	if event.SchemaVersion != 1 || event.EventID == "" || event.TraceID == "" || event.SpanID == "" {
		return fmt.Errorf("invalid lifecycle event identity")
	}
	if event.Source == "" || event.NodeID == "" || event.OccurredAt.IsZero() || event.Sequence <= 0 {
		return fmt.Errorf("incomplete lifecycle event metadata")
	}
	if !IsSupportedPhase(event.Phase) {
		return fmt.Errorf("unsupported lifecycle phase %q", event.Phase)
	}
	return nil
}
