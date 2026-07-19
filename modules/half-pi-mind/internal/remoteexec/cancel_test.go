package remoteexec

import (
	"context"
	"errors"
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

func newCancelAuthority(t *testing.T, handID string, registry *Registry) (*Authority, *wss.SessionConn, *hub.Peer) {
	t.Helper()
	wsHub := hub.New()
	const applicationKey = "22222222222222222222222222222222"
	wsHub.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: "11111111111111111111111111111111", ApplicationKey: applicationKey, PrincipalID: key.Label}, nil
	})
	authority := NewAuthority(wsHub, registry, nil)
	wsHub.OnMessage(authority.HandleHandMessage)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err == nil {
			_ = wsHub.ServeWS(conn)
		}
	}))
	t.Cleanup(server.Close)
	session, err := wss.NewClient("ws" + strings.TrimPrefix(server.URL, "http")).ConnectAndRegister(wss.Credentials{
		Label: handID, Type: hub.PeerHand,
		Token: "11111111111111111111111111111111", ApplicationKey: applicationKey,
		Info: &protocol.HandInfo{Hostname: "test", OS: "linux", Arch: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Conn.Close()
		_ = authority.Close()
	})
	peer := wsHub.PeerByType(hub.PeerHand, handID)
	if peer == nil {
		t.Fatal("registered Hand peer was not retained")
	}
	return authority, session, peer
}

func createBoundSentRun(t *testing.T, registry *Registry, peer *hub.Peer, runID, conversationID string) {
	t.Helper()
	if err := registry.CreateForPeer(runID, conversationID, peer.ID, peer.SessionID(), "exec_command", AuditMetadata{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition(runID, protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition(runID, protocol.RunSent); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityCancelRunChecksOwnershipAndUsesBoundHand(t *testing.T) {
	registry := NewRegistry()
	authority, hand, peer := newCancelAuthority(t, "cancel-hand", registry)
	createBoundSentRun(t, registry, peer, "run-cancel", "conversation-1")

	if _, err := authority.CancelRun(context.Background(), "conversation-2", "run-cancel", "user"); !errors.Is(err, ErrRunNotOwned) {
		t.Fatalf("wrong conversation cancellation error = %v", err)
	}
	if run, _ := registry.Snapshot("run-cancel"); run.Status != protocol.RunSent {
		t.Fatalf("wrong conversation changed run to %s", run.Status)
	}

	result := make(chan Run, 1)
	errorsSeen := make(chan error, 1)
	go func() {
		run, err := authority.CancelRun(context.Background(), "conversation-1", "run-cancel", "user")
		result <- run
		errorsSeen <- err
	}()
	env, err := hand.Read()
	if err != nil || env.Type != protocol.TypeRPCCancel {
		t.Fatalf("cancel envelope = %q, %v", env.Type, err)
	}
	request, err := protocol.DecodePayload[protocol.RPCCancel](&env)
	if err != nil || request.RunID != "run-cancel" || request.Reason != "user" {
		t.Fatalf("cancel request = %+v, %v", request, err)
	}
	if err := hand.SendPayload("cancel-result", protocol.TypeRPCCancelResult, protocol.RPCCancelResult{
		RunID: request.RunID, Status: protocol.CancelCancelled,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case run := <-result:
		if run.Status != protocol.RunCancelled {
			t.Fatalf("cancelled run = %+v", run)
		}
	case <-time.After(time.Second):
		t.Fatal("Authority cancellation did not finish")
	}
	if err := <-errorsSeen; err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityCancelRunCancelledContextDoesNotMutateRun(t *testing.T) {
	registry := NewRegistry()
	authority, _, peer := newCancelAuthority(t, "context-hand", registry)
	createBoundSentRun(t, registry, peer, "run-context", "conversation-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := authority.CancelRun(ctx, "conversation-1", "run-context", "user"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v", err)
	}
	run, _ := registry.Snapshot("run-context")
	if run.Status != protocol.RunSent || run.CancelReason != "" {
		t.Fatalf("cancelled context mutated run: %+v", run)
	}
}

func TestAuthorityCancelRunResultRaceKeepsSingleTerminal(t *testing.T) {
	registry := NewRegistry()
	authority, hand, peer := newCancelAuthority(t, "race-hand", registry)
	createBoundSentRun(t, registry, peer, "run-race", "conversation-1")
	if err := registry.ApplyAcceptedFrom(peer.ID, peer.SessionID(), protocol.RPCAccepted{
		RunID: "run-race", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := authority.CancelRun(context.Background(), "conversation-1", "run-race", "user")
		done <- err
	}()
	env, err := hand.Read()
	if err != nil || env.Type != protocol.TypeRPCCancel {
		t.Fatalf("cancel envelope = %q, %v", env.Type, err)
	}
	if err := hand.SendPayload("result-wins", protocol.TypeRPCResult, protocol.RPCResult{
		RunID: "run-race", Success: true, Output: "done",
	}); err != nil {
		t.Fatal(err)
	}
	if err := hand.SendPayload("late-cancel", protocol.TypeRPCCancelResult, protocol.RPCCancelResult{
		RunID: "run-race", Status: protocol.CancelCancelled,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Authority cancellation did not observe terminal result")
	}
	run, _ := registry.Snapshot("run-race")
	if run.Status != protocol.RunSucceeded || run.Result == nil || run.Result.Output != "done" {
		t.Fatalf("result/cancel race run = %+v", run)
	}
}

func TestAuthorityCancelRunRejectsReplacementConnection(t *testing.T) {
	registry := NewRegistry()
	authority, first, peer := newCancelAuthority(t, "replace-hand", registry)
	createBoundSentRun(t, registry, peer, "run-replaced", "conversation-1")
	_ = first.Conn.Close()
	authority.Hub.RemovePeer(peer)
	if _, err := authority.CancelRun(context.Background(), "conversation-1", "run-replaced", "user"); !errors.Is(err, ErrRunHandOffline) {
		t.Fatalf("disconnected bound Hand error = %v", err)
	}
}
