package main

import (
	"bytes"
	"context"
	"testing"
)

func TestVersionDoesNotRequireConfig(t *testing.T) {
	var output, logs bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, bytes.NewReader(nil), &output, &logs); err != nil {
		t.Fatal(err)
	}
	if output.String() != "half-pi-face version dev\n" || logs.Len() != 0 {
		t.Fatalf("output=%q logs=%q", output.String(), logs.String())
	}
}
