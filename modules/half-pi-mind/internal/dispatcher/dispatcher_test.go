package dispatcher

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

const (
	testToken   = "11111111111111111111111111111111"
	testHandKey = "22222222222222222222222222222222"
	testFaceKey = "33333333333333333333333333333333"
)

type testCredentials struct{}

func (testCredentials) AuthenticateHandCredentialKey(label, token string) (string, error) {
	if label != "same-label" || token != testToken {
		return "", fmt.Errorf("authentication failed")
	}
	return testHandKey, nil
}

func (testCredentials) AuthenticateFaceCredentialKey(label, token string) (string, error) {
	if label != "same-label" || token != testToken {
		return "", fmt.Errorf("authentication failed")
	}
	return testFaceKey, nil
}

type recordingHandHandler struct {
	mu          sync.Mutex
	messages    int
	disconnects int
}

func (h *recordingHandHandler) HandleHandMessage(*hub.Peer, protocol.Envelope) {
	h.mu.Lock()
	h.messages++
	h.mu.Unlock()
}

func (h *recordingHandHandler) HandleHandDisconnect(*hub.Peer) {
	h.mu.Lock()
	h.disconnects++
	h.mu.Unlock()
}

func TestDispatcherSeparatesHandAndFaceWithSameLabel(t *testing.T) {
	h := hub.New()
	hands := &recordingHandHandler{}
	Install(h, testCredentials{}, hands)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			_ = h.ServeWS(conn)
		}
	}))
	t.Cleanup(func() { h.Close(); server.Close() })
	url := "ws" + strings.TrimPrefix(server.URL, "http")

	handConn, err := wss.NewClient(url).ConnectAndRegister(wss.Credentials{
		Label: "same-label", Type: protocol.PeerHand, Token: testToken, ApplicationKey: testHandKey,
		Info: &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handConn.Conn.Close()
	faceConn, err := wss.NewClient(url).ConnectAndRegister(wss.Credentials{
		Label: "same-label", Type: protocol.PeerFace, Token: testToken, ApplicationKey: testFaceKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer faceConn.Conn.Close()

	if h.PeerByType(hub.PeerHand, "same-label") == nil || h.PeerByType(hub.PeerFace, "same-label") == nil {
		t.Fatal("same-label typed peers were not both registered")
	}
	if err := handConn.SendPayload("hand-msg", protocol.TypeHandEvent, protocol.HandEvent{Name: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := faceConn.SendPayload("face-msg", protocol.TypeFaceConversationList, protocol.FaceConversationList{RequestID: "request-1"}); err != nil {
		t.Fatal(err)
	}
	env, response, err := wss.ReadPayload[protocol.FaceError](faceConn)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != protocol.TypeFaceError || response.Code != protocol.FaceErrorInternal || response.RequestID != "request-1" || response.Retryable {
		t.Fatalf("Face response = type %q payload %+v", env.Type, response)
	}

	deadline := time.Now().Add(time.Second)
	for {
		hands.mu.Lock()
		messages := hands.messages
		disconnects := hands.disconnects
		hands.mu.Unlock()
		if messages == 1 {
			if disconnects != 0 {
				t.Fatalf("Face traffic caused %d Hand disconnects", disconnects)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Hand messages = %d, want 1", messages)
		}
		time.Sleep(time.Millisecond)
	}

	h.RemoveByType(hub.PeerFace, "same-label")
	if h.PeerByType(hub.PeerHand, "same-label") == nil {
		t.Fatal("removing Face disconnected same-label Hand")
	}
	hands.mu.Lock()
	disconnects := hands.disconnects
	hands.mu.Unlock()
	if disconnects != 0 {
		t.Fatalf("Face disconnect reached Hand handler %d times", disconnects)
	}
}

func TestDispatcherRejectsInvalidFacePayloadWithoutAccepted(t *testing.T) {
	h := hub.New()
	Install(h, testCredentials{}, &recordingHandHandler{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			_ = h.ServeWS(conn)
		}
	}))
	t.Cleanup(func() { h.Close(); server.Close() })
	faceConn, err := wss.NewClient("ws" + strings.TrimPrefix(server.URL, "http")).ConnectAndRegister(wss.Credentials{
		Label: "same-label", Type: protocol.PeerFace, Token: testToken, ApplicationKey: testFaceKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer faceConn.Conn.Close()
	env := protocol.Envelope{MsgID: "bad", Type: protocol.TypeFaceConversationList, Payload: []byte(`{"request_id":"request-1","unknown":true}`)}
	if err := faceConn.Send(env); err != nil {
		t.Fatal(err)
	}
	responseEnv, response, err := wss.ReadPayload[protocol.FaceError](faceConn)
	if err != nil {
		t.Fatal(err)
	}
	if responseEnv.Type != protocol.TypeFaceError || response.Code != protocol.FaceErrorInvalidRequest {
		t.Fatalf("response = type %q payload %+v", responseEnv.Type, response)
	}
}
