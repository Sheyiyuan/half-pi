package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestRunVersionDoesNotInitializeRuntime(t *testing.T) {
	var output, logs bytes.Buffer
	if err := run([]string{"--version"}, &output, &logs); err != nil {
		t.Fatal(err)
	}
	if output.String() != "half-pi-mind version dev\n" || logs.Len() != 0 {
		t.Fatalf("output=%q logs=%q", output.String(), logs.String())
	}
}

func TestWriteMindReadyPropagatesOutputFailure(t *testing.T) {
	err := writeMindReady(failingWriter{}, mindReady{Type: "mind.ready"})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("writeMindReady error = %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, context.Canceled }
