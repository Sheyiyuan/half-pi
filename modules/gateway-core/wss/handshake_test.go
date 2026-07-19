package wss

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const (
	testHandshakeToken = "00112233445566778899aabbccddeeff"
	testHandshakeKey   = "ffeeddccbbaa99887766554433221100"
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
	keys, err := DeriveSessionKeys(testHandshakeToken, testHandshakeKey, testTranscript())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(keys.Proof[:], keys.ClientToServer[:]) || bytes.Equal(keys.Proof[:], keys.ServerToClient[:]) || bytes.Equal(keys.ClientToServer[:], keys.ServerToClient[:]) {
		t.Fatal("derived keys are not purpose and direction isolated")
	}
	changed := testTranscript()
	changed.Label = "hand-2"
	other, err := DeriveSessionKeys(testHandshakeToken, testHandshakeKey, changed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(keys.Proof[:], other.Proof[:]) {
		t.Fatal("transcript change did not change derived key")
	}
	wrongToken, err := DeriveSessionKeys("11111111111111111111111111111111", testHandshakeKey, testTranscript())
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := DeriveSessionKeys(testHandshakeToken, "22222222222222222222222222222222", testTranscript())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(keys.Proof[:], wrongToken.Proof[:]) || bytes.Equal(keys.Proof[:], wrongKey.Proof[:]) {
		t.Fatal("both token and application key must affect derived keys")
	}
}

func TestRegisterProofRoundTripAndTamper(t *testing.T) {
	transcript := testTranscript()
	keys, _ := DeriveSessionKeys(testHandshakeToken, testHandshakeKey, transcript)
	info := &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: "host", WorkDir: "/workspace"}
	proof, err := NewRegisterProof(keys, transcript, info)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := VerifyRegisterProof(keys, transcript, proof)
	if err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	if claims.HandInfo == nil || *claims.HandInfo != *info {
		t.Fatalf("decrypted Hand info = %+v", claims.HandInfo)
	}
	for name, mutate := range map[string]func(*protocol.RegisterProof, *protocol.HandshakeTranscript){
		"version":      func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { p.ProtocolVersion++ },
		"algorithm":    func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { p.Algorithm = "other" },
		"handshake id": func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { p.HandshakeID = "other" },
		"label":        func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { tr.Label = "other" },
		"peer type":    func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { tr.PeerType = protocol.PeerFace },
		"server id":    func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { tr.ServerID = "other" },
		"session id":   func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { tr.SessionID = "other" },
		"challenge": func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) {
			tr.Challenge = base64.StdEncoding.EncodeToString(make([]byte, 32))
		},
		"transcript ver": func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) { tr.ProtocolVersion++ },
		"proof": func(p *protocol.RegisterProof, tr *protocol.HandshakeTranscript) {
			p.Proof = p.Proof[:len(p.Proof)-1] + "A"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedProof, changedTranscript := proof, transcript
			mutate(&changedProof, &changedTranscript)
			if _, err := VerifyRegisterProof(keys, changedTranscript, changedProof); err == nil {
				t.Fatal("tampered proof was accepted")
			}
		})
	}
}

func TestRegisterProofRoleAndClaimsValidation(t *testing.T) {
	faceTranscript := testTranscript()
	faceTranscript.PeerType = protocol.PeerFace
	faceTranscript.Label = "face-1"
	keys, err := DeriveSessionKeys(testHandshakeToken, testHandshakeKey, faceTranscript)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegisterProof(keys, faceTranscript, &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: "host"}); err == nil {
		t.Fatal("Face proof accepted Hand info")
	}
	for name, raw := range map[string]string{
		"face hand info":  `{"challenge":"` + faceTranscript.Challenge + `","hand_info":{"os":"linux","arch":"amd64","hostname":"host","work_dir":""}}`,
		"face null info":  `{"challenge":"` + faceTranscript.Challenge + `","hand_info":null}`,
		"wrong challenge": `{"challenge":"` + base64.StdEncoding.EncodeToString(make([]byte, 32)) + `"}`,
		"unknown claim":   `{"challenge":"` + faceTranscript.Challenge + `","unknown":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			proof := encryptProofPlain(t, keys, faceTranscript, []byte(raw))
			if _, err := VerifyRegisterProof(keys, faceTranscript, proof); err == nil {
				t.Fatal("invalid Face proof claims were accepted")
			}
		})
	}

	handTranscript := testTranscript()
	handKeys, err := DeriveSessionKeys(testHandshakeToken, testHandshakeKey, handTranscript)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegisterProof(handKeys, handTranscript, nil); err == nil {
		t.Fatal("Hand proof accepted missing Hand info")
	}
	proof := encryptProofPlain(t, handKeys, handTranscript, []byte(`{"challenge":"`+handTranscript.Challenge+`"}`))
	if _, err := VerifyRegisterProof(handKeys, handTranscript, proof); err == nil {
		t.Fatal("encrypted Hand proof accepted missing Hand info")
	}
}

func encryptProofPlain(t *testing.T, keys SessionKeys, transcript protocol.HandshakeTranscript, plain []byte) protocol.RegisterProof {
	t.Helper()
	aad, err := proofAAD(transcript)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewAES128GCM(keys.Proof[:])
	if err != nil {
		t.Fatal(err)
	}
	data, err := cipher.Encrypt(plain, aad)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.RegisterProof{
		ProtocolVersion: protocol.ProtocolVersion,
		HandshakeID:     transcript.HandshakeID,
		Algorithm:       protocol.HandshakeAlgorithm,
		Proof:           base64.StdEncoding.EncodeToString(data),
	}
}
