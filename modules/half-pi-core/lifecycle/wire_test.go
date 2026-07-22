package lifecycle

import (
	"bytes"
	"testing"
)

func TestRedactedWireFixtureIsStrictAndNeverContainsSensitiveData(t *testing.T) {
	meta := NewMeta(SourceMind).WithConversation("conversation-1").WithGroup("group-1").WithNode("mind").EventMeta(1)
	event := RedactedEvent{
		Meta: meta, Phase: PhaseToolFrozen, ResourceName: "exec_command",
		InputDigest: "sha256:test", Sensitive: map[string]any{"args": "secret-value"},
	}
	encoded, err := EncodeRedactedEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("secret-value")) || bytes.Contains(encoded, []byte("sensitive")) {
		t.Fatalf("wire leaked sensitive data: %s", encoded)
	}
	decoded, err := DecodeRedactedEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Phase != PhaseToolFrozen || decoded.ResourceName != "exec_command" || decoded.InputDigest != "sha256:test" {
		t.Fatalf("wire round trip = %#v", decoded)
	}
	withUnknown := append(encoded[:len(encoded)-1], []byte(`,"unknown":true}`)...)
	if _, err := DecodeRedactedEvent(withUnknown); err == nil {
		t.Fatal("wire accepted unknown field")
	}
}

func TestAllLifecyclePhasesUseSameWireContract(t *testing.T) {
	sequence := int64(0)
	for phase := range phaseOrder {
		sequence++
		meta := NewMeta(SourceHand).WithNode("hand-1").EventMeta(sequence)
		encoded, err := EncodeRedactedEvent(RedactedEvent{Meta: meta, Phase: phase})
		if err != nil {
			t.Fatalf("phase %s: %v", phase, err)
		}
		if _, err := DecodeRedactedEvent(encoded); err != nil {
			t.Fatalf("phase %s round trip: %v", phase, err)
		}
	}
}
