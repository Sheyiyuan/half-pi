//go:build windows

package adminipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestNamedPipeRoundTripAndShutdown(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "management.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := management.New(db, nil, management.Runtime{Mode: "test", HubEnabled: false})
	endpoint := `\\.\pipe\half-pi-admin-test-` + uuid.NewString()
	server, err := Start(endpoint, service)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := Call(ctx, endpoint, Request{
		Version: Version, RequestID: uuid.NewString(), Operation: "status.get",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("status response = %+v", response)
	}
	raw, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var status management.StatusResult
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "running" || status.Mode != "test" || status.HubEnabled {
		t.Fatalf("status = %+v", status)
	}

	done := make(chan error, 1)
	go func() { done <- server.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("named pipe shutdown blocked")
	}
}

func TestNamedPipeDeadlineAppliesToPendingRead(t *testing.T) {
	endpoint := `\\.\pipe\half-pi-admin-deadline-` + uuid.NewString()
	listener, err := listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan interface {
		Read([]byte) (int, error)
		Close() error
	}, 1)
	acceptErrors := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			acceptErrors <- acceptErr
			return
		}
		accepted <- conn
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := dial(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var serverConn interface {
		Read([]byte) (int, error)
		Close() error
	}
	select {
	case serverConn = <-accepted:
	case err := <-acceptErrors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer serverConn.Close()

	readDone := make(chan error, 1)
	go func() {
		_, readErr := client.Read(make([]byte, 1))
		readDone <- readErr
	}()
	time.Sleep(20 * time.Millisecond)
	if err := client.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("pending read error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending read ignored updated deadline")
	}
}

func TestNamedPipeDACLAllowsOnlyCurrentUserAndSystem(t *testing.T) {
	endpoint := `\\.\pipe\half-pi-admin-acl-` + uuid.NewString()
	listener, err := listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan netConnResult, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		accepted <- netConnResult{conn: conn, err: acceptErr}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := dial(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result := <-accepted
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer result.conn.Close()

	wrapped, ok := client.(*pipeConn)
	if !ok {
		t.Fatalf("client type = %T", client)
	}
	handleConn, ok := wrapped.Conn.(interface{ Fd() uintptr })
	if !ok {
		t.Fatalf("raw client type = %T", wrapped.Conn)
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(handleConn.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("D:P(A;;FA;;;SY)(A;;FA;;;%s)", current.Uid)
	if got := descriptor.String(); got != want {
		t.Fatalf("pipe DACL = %q, want %q", got, want)
	}
}

type netConnResult struct {
	conn interface {
		Close() error
	}
	err error
}
