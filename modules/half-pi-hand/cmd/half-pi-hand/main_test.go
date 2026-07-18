package main

import (
	"errors"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

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
