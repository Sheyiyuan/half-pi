package wss_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

func handCredentials(label string) wss.Credentials {
	return wss.Credentials{
		Label: label, Type: protocol.PeerHand, Token: testToken, ApplicationKey: testKey,
		Info: &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: "host", WorkDir: "/workspace"},
	}
}

// recordedSession 是一次真实会话中被动录制到的 server→client 帧。
type recordedSession struct {
	challenge  []byte
	registered []byte
	business   []byte
}

// recordSession 通过中间人代理录制一次完整握手及一条业务消息的 server→client 帧。
func recordSession(t *testing.T, label string) recordedSession {
	t.Helper()
	h := hub.New()
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: testToken, ApplicationKey: testKey, PrincipalID: key.Label}, nil
	})
	peers := make(chan *hub.Peer, 1)
	h.OnConnect(func(peer *hub.Peer) { peers <- peer })
	upstreamURL, closeUpstream := startHub(t, h)
	defer closeUpstream()

	frames := make(chan []byte, 16)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstream, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer downstream.Close()
		upstream, _, err := websocket.DefaultDialer.Dial(upstreamURL, nil)
		if err != nil {
			return
		}
		defer upstream.Close()
		go func() {
			for {
				_, raw, err := downstream.ReadMessage()
				if err != nil || upstream.WriteMessage(websocket.TextMessage, raw) != nil {
					return
				}
			}
		}()
		for {
			_, raw, err := upstream.ReadMessage()
			if err != nil {
				return
			}
			frames <- append([]byte(nil), raw...)
			if downstream.WriteMessage(websocket.TextMessage, raw) != nil {
				return
			}
		}
	}))
	defer proxy.Close()

	session, err := wss.NewClient("ws"+strings.TrimPrefix(proxy.URL, "http")).ConnectAndRegister(handCredentials(label))
	if err != nil {
		t.Fatalf("录制握手失败: %v", err)
	}
	env, err := protocol.NewEnvelope("", protocol.TypeRPC, map[string]any{"tool": "exec_command"})
	if err != nil {
		t.Fatal(err)
	}
	if err := (<-peers).SendContext(t.Context(), *env); err != nil {
		t.Fatal(err)
	}
	if got, err := session.Read(); err != nil || got.Type != protocol.TypeRPC {
		t.Fatalf("读取业务消息失败: %v", err)
	}
	_ = session.Conn.Close()

	recorded := recordedSession{challenge: <-frames, registered: <-frames, business: <-frames}
	return recorded
}

// replayServer 启动只回放录制帧的恶意服务端，不持有任何秘密。
func replayServer(t *testing.T, challenge, registered, business []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil { // register，丢弃
			return
		}
		if conn.WriteMessage(websocket.TextMessage, challenge) != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil { // proof，攻击者无法验证，丢弃
			return
		}
		if conn.WriteMessage(websocket.TextMessage, registered) != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, business)
		time.Sleep(time.Second)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// TestReplayRecordedServerStreamRejected 断言改写 expires_at 的录制流重放被拒绝。
// expires_at 必须参与密钥派生，否则攻击者可无秘密地复现客户端会话密钥。
func TestReplayRecordedServerStreamRejected(t *testing.T) {
	recorded := recordSession(t, "hand-replay-expiry")

	var env protocol.Envelope
	if err := json.Unmarshal(recorded.challenge, &env); err != nil {
		t.Fatal(err)
	}
	var challenge protocol.RegisterChallenge
	if err := json.Unmarshal(env.Payload, &challenge); err != nil {
		t.Fatal(err)
	}
	challenge.ExpiresAt = time.Now().Add(5 * time.Second).UnixMilli()
	payload, err := json.Marshal(challenge)
	if err != nil {
		t.Fatal(err)
	}
	env.Payload = payload
	forged, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}

	url := replayServer(t, forged, recorded.registered, recorded.business)
	session, err := wss.NewClient(url).ConnectAndRegister(handCredentials("hand-replay-expiry"))
	if err == nil {
		_ = session.Conn.Close()
		t.Fatal("改写 expires_at 的重放握手被接受")
	}
}

// TestReplayRecordedServerStreamRejectedWithinExpiry 断言在原始有效期内原样重放也被拒绝。
// 仅把 expires_at 纳入 transcript 挡不住这一条：客户端必须自行贡献握手熵。
func TestReplayRecordedServerStreamRejectedWithinExpiry(t *testing.T) {
	recorded := recordSession(t, "hand-replay-window")
	url := replayServer(t, recorded.challenge, recorded.registered, recorded.business)
	session, err := wss.NewClient(url).ConnectAndRegister(handCredentials("hand-replay-window"))
	if err == nil {
		_ = session.Conn.Close()
		t.Fatal("有效期窗口内的重放握手被接受")
	}
}
