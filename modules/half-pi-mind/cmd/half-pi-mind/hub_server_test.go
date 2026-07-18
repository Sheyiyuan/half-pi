package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
)

func TestStartHubServerUsesDynamicPortAndWritesReady(t *testing.T) {
	server, err := startHubServer(config.ServerConfig{Host: "127.0.0.1", Port: 0}, hub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	ready := server.ready(123)
	parsed, err := url.Parse(ready.WSURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port <= 0 || parsed.Hostname() != "127.0.0.1" || parsed.Path != "/ws" {
		t.Fatalf("ready = %+v", ready)
	}
	var output bytes.Buffer
	if err := writeMindReady(&output, ready); err != nil {
		t.Fatal(err)
	}
	var decoded mindReady
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded != ready {
		t.Fatalf("decoded ready = %+v, %v", decoded, err)
	}
}

func TestStartHubServerReturnsBindFailureSynchronously(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	portNumber, _ := strconv.Atoi(port)
	_, err = startHubServer(config.ServerConfig{Host: "127.0.0.1", Port: portNumber}, hub.New())
	if err == nil || !strings.Contains(err.Error(), "listen Mind Hub") {
		t.Fatalf("startHubServer error = %v", err)
	}
}
