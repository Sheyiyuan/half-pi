package wss

import (
	"bytes"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func testTranscript() protocol.HandshakeTranscript {
	return protocol.HandshakeTranscript{
		ProtocolVersion: protocol.ProtocolVersion,
		PeerType:        protocol.PeerHand,
		Label:           "hand-1",
		HandshakeID:     "00112233445566778899aabbccddeeff",
		ServerID:        "mind",
		SessionID:       "ffeeddccbbaa99887766554433221100",
		Challenge:       "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
	}
}

func TestDeriveSessionKeysDirectionAndPurposeIsolation(t *testing.T) {
	keys, err := DeriveSessionKeys("00112233445566778899aabbccddeeff", testTranscript())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(keys.Proof[:], keys.ClientToServer[:]) || bytes.Equal(keys.Proof[:], keys.ServerToClient[:]) || bytes.Equal(keys.ClientToServer[:], keys.ServerToClient[:]) {
		t.Fatal("derived keys are not purpose and direction isolated")
	}
	changed := testTranscript()
	changed.Label = "hand-2"
	other, err := DeriveSessionKeys("00112233445566778899aabbccddeeff", changed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(keys.Proof[:], other.Proof[:]) {
		t.Fatal("transcript change did not change derived key")
	}
}

func TestRegisterProofRoundTripAndTamper(t *testing.T) {
	transcript := testTranscript()
	keys, _ := DeriveSessionKeys("00112233445566778899aabbccddeeff", transcript)
	proof, err := NewRegisterProof(keys, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRegisterProof(keys, transcript, proof); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	for name, mutate := range map[string]func(*protocol.RegisterProof, *protocol.HandshakeTranscript){
		"version":      func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { p.ProtocolVersion++ },
		"algorithm":    func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { p.Algorithm = "other" },
		"handshake id": func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { p.HandshakeID = "other" },
		"aad label":    func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { tr.Label = "other" },
		"proof": func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) {
			p.Proof = p.Proof[:len(p.Proof)-1] + "A"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedProof, changedTranscript := proof, transcript
			mutate(&changedProof, &changedTranscript)
			if err := VerifyRegisterProof(keys, changedTranscript, changedProof); err == nil {
				t.Fatal("tampered proof was accepted")
			}
		})
	}
}
