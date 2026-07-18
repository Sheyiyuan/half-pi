package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

func TestRunVersionDoesNotRequireConfig(t *testing.T) {
	var output, logs bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &output, &logs); err != nil {
		t.Fatal(err)
	}
	if output.String() != "half-pi-hand version dev\n" || logs.Len() != 0 {
		t.Fatalf("output=%q logs=%q", output.String(), logs.String())
	}
}

func TestRunRequiresOutputStreams(t *testing.T) {
	if err := run(context.Background(), nil, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("run accepted nil output")
	}
	if err := run(context.Background(), nil, &bytes.Buffer{}, nil); err == nil {
		t.Fatal("run accepted nil logs")
	}
}

func TestWriteHandReadyIsStructuredAndContainsNoCredentials(t *testing.T) {
	ready := handReady{Type: "hand.ready", PID: 123, HandID: "hand-1"}
	var output bytes.Buffer
	if err := writeHandReady(&output, ready); err != nil {
		t.Fatal(err)
	}
	var decoded handReady
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded != ready {
		t.Fatalf("decoded ready = %+v, %v", decoded, err)
	}
	for _, secret := range []string{"token", "application_key"} {
		if bytes.Contains(output.Bytes(), []byte(secret)) {
			t.Fatalf("ready output contains %q: %s", secret, output.Bytes())
		}
	}
}

func TestWriteHandReadyPropagatesOutputFailure(t *testing.T) {
	err := writeHandReady(handFailingWriter{}, handReady{Type: "hand.ready"})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("writeHandReady error = %v", err)
	}
}

type handFailingWriter struct{}

func (handFailingWriter) Write([]byte) (int, error) { return 0, context.Canceled }

func TestShouldRetryHandshakeFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "network", err: errors.New("connection refused"), want: true},
		{name: "duplicate", err: &wss.HandshakeError{Code: "duplicate_peer"}, want: true},
		{name: "authentication", err: &wss.HandshakeError{Code: "authentication_failed"}, want: false},
		{name: "protocol", err: &wss.HandshakeError{Code: "unsupported_protocol"}, want: false},
		{name: "unknown", err: &wss.HandshakeError{Code: "future_error"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldRetry(test.err); got != test.want {
				t.Fatalf("shouldRetry() = %v, want %v", got, test.want)
			}
		})
	}
}
