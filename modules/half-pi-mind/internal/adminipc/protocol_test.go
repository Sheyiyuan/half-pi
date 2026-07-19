package adminipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
)

func TestDecodeRequestRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	requestID := uuid.NewString()
	valid := `{"version":1,"request_id":"` + requestID + `","operation":"status.get"}`
	if _, err := decodeRequest([]byte(valid + "\n")); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if _, err := decodeRequest([]byte(`{"version":1,"request_id":"` + requestID + `","operation":"status.get","extra":true}`)); err == nil {
		t.Fatal("unknown request field accepted")
	}
	if _, err := decodeRequest([]byte(valid + " {}")); err == nil {
		t.Fatal("trailing request data accepted")
	}
}

func TestDecodeRequestRejectsInvalidVersionAndRequestID(t *testing.T) {
	for name, raw := range map[string]string{
		"version":    `{"version":2,"request_id":"` + uuid.NewString() + `","operation":"status.get"}`,
		"request_id": `{"version":1,"request_id":"not-a-uuid","operation":"status.get"}`,
		"operation":  `{"version":1,"request_id":"` + uuid.NewString() + `","operation":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRequest([]byte(raw)); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestDecodeParamsIsStrict(t *testing.T) {
	type params struct {
		Label string `json:"label"`
	}
	if got, err := decodeParams[params](json.RawMessage(`{"label":"desktop"}`)); err != nil || got.Label != "desktop" {
		t.Fatalf("valid params = %+v, err = %v", got, err)
	}
	for name, raw := range map[string]json.RawMessage{
		"unknown":  json.RawMessage(`{"label":"desktop","extra":true}`),
		"trailing": json.RawMessage(`{"label":"desktop"} {}`),
		"null":     json.RawMessage(`null`),
		"array":    json.RawMessage(`[]`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeParams[params](raw); err == nil {
				t.Fatal("invalid params accepted")
			}
		})
	}
}

func TestReadOnlyOperationsRejectParams(t *testing.T) {
	server := &Server{}
	for _, operation := range []string{"status.get", "peers.list", "hand.list", "face.list"} {
		t.Run(operation, func(t *testing.T) {
			_, err := server.dispatch(Request{Operation: operation, Params: json.RawMessage(`{"unexpected":true}`)})
			var managed *management.Error
			if !errors.As(err, &managed) || managed.Code != "invalid_argument" {
				t.Fatalf("unexpected params error = %v", err)
			}
		})
	}
}

func TestWriteResponseIncludesProtocolBinding(t *testing.T) {
	var conn testConn
	writeResponse(&conn, Response{Version: Version, RequestID: "request", OK: true, Result: map[string]any{"ok": true}})
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(conn.buf.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	if response["version"] != float64(Version) || response["request_id"] != "request" || response["ok"] != true {
		t.Fatalf("response binding = %#v", response)
	}
}

func TestReadJSONLineRejectsOversizedMessage(t *testing.T) {
	raw := strings.Repeat("x", MaxMessageSize) + "\n"
	if _, err := readJSONLine(strings.NewReader(raw)); err == nil {
		t.Fatal("oversized message accepted")
	}
}

func TestReadJSONLineRejectsBufferedSecondRequest(t *testing.T) {
	if _, err := readJSONLine(strings.NewReader("{}\n{}\n")); err == nil {
		t.Fatal("second request on one connection was accepted")
	}
}

type testConn struct {
	buf bytes.Buffer
}

func (c *testConn) Read([]byte) (int, error) { return 0, io.EOF }

func (c *testConn) Write(p []byte) (int, error) { return c.buf.Write(p) }

func (c *testConn) Close() error { return nil }

func (c *testConn) LocalAddr() net.Addr { return testAddr("local") }

func (c *testConn) RemoteAddr() net.Addr { return testAddr("remote") }

func (c *testConn) SetDeadline(time.Time) error { return nil }

func (c *testConn) SetReadDeadline(time.Time) error { return nil }

func (c *testConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

func (a testAddr) Network() string { return "test" }

func (a testAddr) String() string { return string(a) }
