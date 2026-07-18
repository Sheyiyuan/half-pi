//go:build !windows

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

const (
	handID = "e2e-hand"

	simpleRequestID = "chat-persist"
	markerRequestID = "chat-remote-marker"
	cancelRequestID = "chat-cancel-run"
	taskRequestID   = "chat-background-task"

	simplePrompt = "persist across faces"
	markerPrompt = "write remote marker"
	cancelPrompt = "cancel remote command"
	taskPrompt   = "start background task"

	simpleAnswer = "persisted answer"
	markerAnswer = "remote marker written"
	cancelAnswer = "cancel observed"
	taskAnswer   = "background task started"
)

type mindReady struct {
	Type    string `json:"type"`
	PID     int    `json:"pid"`
	Address string `json:"address"`
	WSURL   string `json:"ws_url"`
}

type handReady struct {
	Type   string `json:"type"`
	PID    int    `json:"pid"`
	HandID string `json:"hand_id"`
}

type processFixture struct {
	home           string
	workDir        string
	database       string
	handConfig     string
	faceConfig     map[string]string
	credentials    map[string]*store.FaceCredential
	handCredential *store.Credential
}

func TestFaceAlphaRealProcessE2E(t *testing.T) {
	t.Parallel()
	temporary := t.TempDir()
	binaries := buildTestBinaries(t, filepath.Join(temporary, "bin"))
	fixture := prepareProcessFixture(t, temporary)

	mind := startTestProcess(t, "mind", binaries.mind, fixture.workDir, fixture.home)
	ready := awaitReady(t, mind, func(value mindReady) bool { return value.Type == "mind.ready" })
	if ready.PID != mind.cmd.Process.Pid || ready.Address == "" || ready.WSURL == "" {
		t.Fatalf("Mind ready = %+v", ready)
	}
	writeHandConfig(t, fixture.handConfig, ready.WSURL, fixture.handCredential, fixture.workDir, filepath.Join(temporary, "hand-tasks"))
	for label, credential := range fixture.credentials {
		writeFaceConfig(t, fixture.faceConfig[label], ready.WSURL, credential, "headless")
	}

	hand := startTestProcess(t, "hand", binaries.hand, fixture.workDir, fixture.home, "--config", fixture.handConfig)
	handStatus := awaitReady(t, hand, func(value handReady) bool { return value.Type == "hand.ready" })
	if handStatus.PID != hand.cmd.Process.Pid || handStatus.HandID != handID {
		t.Fatalf("Hand ready = %+v", handStatus)
	}

	faceA := startHeadlessFace(t, binaries.face, fixture.workDir, fixture.home, fixture.faceConfig["face-a"], "face-a-1")
	faceB := startHeadlessFace(t, binaries.face, fixture.workDir, fixture.home, fixture.faceConfig["face-b"], "face-b")
	faceC := startHeadlessFace(t, binaries.face, fixture.workDir, fixture.home, fixture.faceConfig["face-c"], "face-c")

	conversationID := createConversation(t, faceA)
	requestSnapshot(t, faceA, "snapshot-a-initial", conversationID)
	subscribe(t, faceA, "subscribe-a-initial", conversationID, nil)
	requestSnapshot(t, faceB, "snapshot-b-initial", conversationID)
	subscribe(t, faceB, "subscribe-b", conversationID, nil)
	subscribe(t, faceC, "subscribe-c-approvals", conversationID, []protocol.FaceEventType{
		protocol.FaceEventApprovalRequested, protocol.FaceEventApprovalResolved,
	})

	simpleResult := runChat(t, faceA, simpleRequestID, conversationID, simplePrompt)
	requireChatResult(t, simpleResult, simpleAnswer)
	faceA.stop(t)

	persisted := requestSnapshot(t, faceB, "snapshot-b-persisted", conversationID)
	requireSnapshotContents(t, persisted, simplePrompt, simpleAnswer)

	faceA = startHeadlessFace(t, binaries.face, fixture.workDir, fixture.home, fixture.faceConfig["face-a"], "face-a-2")
	faceA.send(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: simpleRequestID, ConversationID: conversationID, Content: simplePrompt,
	})
	replayedSimple := awaitResult(t, faceA, simpleRequestID)
	requireChatResult(t, replayedSimple, simpleAnswer)
	faceA.send(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: simpleRequestID, ConversationID: conversationID, Content: "conflicting replay",
	})
	conflict := awaitFaceError(t, faceA, simpleRequestID)
	if conflict.Code != protocol.FaceErrorRequestConflict || conflict.Retryable {
		t.Fatalf("conflicting Chat response = %+v", conflict)
	}
	subscribe(t, faceA, "subscribe-a-marker", conversationID, nil)

	faceA.send(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: markerRequestID, ConversationID: conversationID, Content: markerPrompt,
	})
	awaitAccepted(t, faceA, markerRequestID, protocol.FaceOperationChat)
	faceA.stop(t)
	markerApproval := awaitApproval(t, faceC, markerRequestID, "write_file")
	forbiddenResolve(t, faceB, markerApproval.ApprovalID)
	resolveApproval(t, faceC, "resolve-marker", markerApproval.ApprovalID)
	markerChanged := awaitRunStatus(t, faceB, markerApproval.RunID, protocol.RunSucceeded)
	awaitChatCompleted(t, faceB, markerRequestID)
	markerBytes, err := os.ReadFile(filepath.Join(fixture.workDir, "remote-marker.txt"))
	if err != nil || string(markerBytes) != "written by process e2e" {
		t.Fatalf("remote marker = %q, %v", markerBytes, err)
	}
	markerRunView := getRun(t, faceB, "get-marker-run", conversationID, markerApproval.RunID)
	if markerRunView.Status != markerChanged.Status || markerRunView.HandID != handID || markerRunView.Tool != "write_file" {
		t.Fatalf("marker run views disagree: event=%+v get=%+v", markerChanged, markerRunView)
	}

	faceA = startHeadlessFace(t, binaries.face, fixture.workDir, fixture.home, fixture.faceConfig["face-a"], "face-a-3")
	faceA.send(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: markerRequestID, ConversationID: conversationID, Content: markerPrompt,
	})
	replayedMarker := awaitResult(t, faceA, markerRequestID)
	requireChatResult(t, replayedMarker, markerAnswer)
	subscribe(t, faceA, "subscribe-a-cancel", conversationID, nil)

	faceA.send(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: cancelRequestID, ConversationID: conversationID, Content: cancelPrompt,
	})
	awaitAccepted(t, faceA, cancelRequestID, protocol.FaceOperationChat)
	cancelApproval := awaitApproval(t, faceC, cancelRequestID, "exec_command")
	resolveApproval(t, faceC, "resolve-cancel", cancelApproval.ApprovalID)
	awaitRunStatus(t, faceB, cancelApproval.RunID, protocol.RunRunning)
	cancelRun(t, faceA, "cancel-running-run", conversationID, cancelApproval.RunID)
	cancelChanged := awaitRunStatus(t, faceB, cancelApproval.RunID, protocol.RunCancelled)
	cancelChatResult := awaitResult(t, faceA, cancelRequestID)
	requireChatResult(t, cancelChatResult, cancelAnswer)
	cancelRunView := getRun(t, faceA, "get-cancelled-run", conversationID, cancelApproval.RunID)
	if cancelRunView.Status != cancelChanged.Status {
		t.Fatalf("cancel run views disagree: event=%+v get=%+v", cancelChanged, cancelRunView)
	}

	faceA.send(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: taskRequestID, ConversationID: conversationID, Content: taskPrompt,
	})
	awaitAccepted(t, faceA, taskRequestID, protocol.FaceOperationChat)
	taskApproval := awaitApproval(t, faceC, taskRequestID, "exec_command")
	resolveApproval(t, faceC, "resolve-background", taskApproval.ApprovalID)
	runningTask := awaitTaskStatus(t, faceB, taskApproval.RunID, protocol.TaskRunning)
	if runningTask.TaskID != taskApproval.RunID {
		t.Fatalf("background task ID = %q, run ID = %q", runningTask.TaskID, taskApproval.RunID)
	}
	faceA.stop(t)
	if err := os.WriteFile(filepath.Join(fixture.workDir, "release-background"), []byte("release\n"), 0600); err != nil {
		t.Fatal(err)
	}
	terminalTask := reconcileTask(t, faceB, conversationID, runningTask.TaskID)
	if terminalTask.Status != protocol.TaskSucceeded || terminalTask.Stale {
		t.Fatalf("reconciled task = %+v", terminalTask)
	}

	faceA = startHeadlessFace(t, binaries.face, fixture.workDir, fixture.home, fixture.faceConfig["face-a"], "face-a-4")
	faceA.send(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: taskRequestID, ConversationID: conversationID, Content: taskPrompt,
	})
	taskResult := awaitResult(t, faceA, taskRequestID)
	requireChatResult(t, taskResult, taskAnswer)
	finalSnapshot := requestSnapshot(t, faceA, "snapshot-a-final", conversationID)
	requireSnapshotContents(t, finalSnapshot,
		simplePrompt, simpleAnswer, markerPrompt, markerAnswer,
		cancelPrompt, cancelAnswer, taskPrompt, taskAnswer,
	)
	finalTask := findSnapshotTask(t, finalSnapshot, terminalTask.TaskID)
	if finalTask.Status != terminalTask.Status || finalTask.Stale || finalTask.ArgsDigest != terminalTask.ArgsDigest {
		t.Fatalf("snapshot task = %+v, formal task = %+v", finalTask, terminalTask)
	}
	backgroundRunView := getRun(t, faceA, "get-background-run", conversationID, taskApproval.RunID)
	if backgroundRunView.RunID != terminalTask.TaskID || backgroundRunView.Status != protocol.RunSucceeded {
		t.Fatalf("background run view = %+v, task = %+v", backgroundRunView, terminalTask)
	}

	writeFaceConfig(t, fixture.faceConfig["face-tui"], ready.WSURL, fixture.credentials["face-tui"], "tui")
	tui := startTestProcess(t, "face-tui", binaries.face, fixture.workDir, fixture.home, "--config", fixture.faceConfig["face-tui"])
	if _, err := fmt.Fprintf(tui.stdin, "/open %s\n", conversationID); err != nil {
		t.Fatalf("write TUI command: %v", err)
	}
	awaitOutputContains(t, tui, simpleAnswer, markerAnswer, cancelAnswer, taskAnswer)
	if err := tui.closeInputAndWait(); err != nil {
		t.Fatalf("stop TUI: %v\n%s", err, tui.diagnostics())
	}

	faceA.stop(t)
	faceB.stop(t)
	faceC.stop(t)
	if err := hand.interruptAndWait(); err != nil {
		t.Fatalf("stop Hand: %v\n%s", err, hand.diagnostics())
	}
	if err := mind.interruptAndWait(); err != nil {
		t.Fatalf("stop Mind: %v\n%s", err, mind.diagnostics())
	}

	auditProcessState(t, fixture, conversationID, finalSnapshot,
		map[string]protocol.FaceResult{
			simpleRequestID: simpleResult, markerRequestID: replayedMarker,
			cancelRequestID: cancelChatResult, taskRequestID: taskResult,
		},
		map[string]protocol.ApprovalRequest{
			markerRequestID: markerApproval, cancelRequestID: cancelApproval, taskRequestID: taskApproval,
		},
		map[string]protocol.RemoteRunSummary{
			markerRequestID: markerRunView, cancelRequestID: cancelRunView, taskRequestID: backgroundRunView,
		},
		terminalTask,
	)
}

func prepareProcessFixture(t *testing.T, root string) processFixture {
	t.Helper()
	home := filepath.Join(root, "home")
	halfPiHome := filepath.Join(home, ".half-pi")
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(halfPiHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir, 0700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(halfPiHome, "db", "half-pi.db")
	db, err := store.New(database)
	if err != nil {
		t.Fatal(err)
	}
	handCredential, err := db.AddHandCredential(handID)
	if err != nil {
		t.Fatal(err)
	}
	credentials := make(map[string]*store.FaceCredential)
	faceScopes := map[string][]protocol.FaceScope{
		"face-a": {
			protocol.FaceScopeChat, protocol.FaceScopeSessionsRead, protocol.FaceScopeSessionsWrite,
			protocol.FaceScopeRunsRead, protocol.FaceScopeRunsCancel, protocol.FaceScopeHandsRead,
			protocol.FaceScopeTasksRead,
		},
		"face-b": {
			protocol.FaceScopeChat, protocol.FaceScopeSessionsRead, protocol.FaceScopeRunsRead,
			protocol.FaceScopeHandsRead, protocol.FaceScopeTasksRead,
		},
		"face-c":   {protocol.FaceScopeSessionsRead, protocol.FaceScopeApprove},
		"face-tui": {protocol.FaceScopeSessionsRead},
	}
	for label, scopes := range faceScopes {
		credential, addErr := db.AddFaceToken(label, scopes)
		if addErr != nil {
			t.Fatal(addErr)
		}
		credentials[label] = credential
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fixtureDir := filepath.Join(halfPiHome, "fixtures")
	if err := os.MkdirAll(fixtureDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fixtureDir, "face-e2e.json"), scriptedFixture(), 0600)
	writeFile(t, filepath.Join(halfPiHome, "config.toml"), mindConfig(), 0600)
	faceConfig := make(map[string]string, len(credentials))
	for label := range credentials {
		dir := filepath.Join(root, label)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		faceConfig[label] = filepath.Join(dir, "config.toml")
	}
	handDir := filepath.Join(root, "hand")
	if err := os.MkdirAll(handDir, 0700); err != nil {
		t.Fatal(err)
	}
	return processFixture{
		home: home, workDir: workDir, database: database,
		handConfig: filepath.Join(handDir, "config.toml"), faceConfig: faceConfig,
		credentials: credentials, handCredential: handCredential,
	}
}

func mindConfig() string {
	return `[server]
enabled = true
host = "127.0.0.1"
port = 0

[storage]
data_dir = ""
log_dir = ""

[llm]
default_provider = "fixture"
default_model = "fixture-model"

[[llm.providers]]
name = "fixture"
adapter = "scripted"
script_path = "fixtures/face-e2e.json"

[[llm.models]]
id = "fixture-model"
provider = "fixture"
capabilities = []
max_tokens = 4096
temperature = 0
input_price_per_1k = 0
output_price_per_1k = 0
`
}

func scriptedFixture() string {
	fixture := map[string]any{
		"version": 1,
		"steps": []any{
			scriptStep("user", simplePrompt, "", map[string]any{"content": simpleAnswer}),
			scriptStep("user", markerPrompt, "", toolResponse("list-hands", "list_hands", map[string]any{})),
			scriptStep("tool", "", "list-hands", toolResponse("hand-info", "get_hand_info", map[string]any{"hand_id": handID})),
			scriptStep("tool", "", "hand-info", toolResponse("marker-write", "use_hand", map[string]any{
				"hand_id": handID, "tool": "write_file",
				"args": map[string]any{"path": "remote-marker.txt", "content": "written by process e2e"},
			})),
			scriptStep("tool", "", "marker-write", map[string]any{"content": markerAnswer}),
			scriptStep("user", cancelPrompt, "", toolResponse("cancel-run", "use_hand", map[string]any{
				"hand_id": handID, "tool": "exec_command", "confirm": true, "timeout_ms": 60000,
				"args": map[string]any{"command": "sleep 30"},
			})),
			scriptStep("tool", "", "cancel-run", map[string]any{"content": cancelAnswer}),
			scriptStep("user", taskPrompt, "", toolResponse("background-run", "use_hand", map[string]any{
				"hand_id": handID, "tool": "exec_command", "confirm": true, "background": true,
				"task_timeout_ms": 30000,
				"args":            map[string]any{"command": "while [ ! -f release-background ]; do sleep 0.05; done; printf background-done"},
			})),
			scriptStep("tool", "", "background-run", map[string]any{"content": taskAnswer}),
		},
	}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(encoded) + "\n"
}

func scriptStep(role, content, toolID string, response map[string]any) map[string]any {
	expectation := map[string]any{"last_role": role}
	if content != "" {
		expectation["last_content"] = content
	}
	if toolID != "" {
		expectation["last_tool_id"] = toolID
	}
	return map[string]any{"expect": expectation, "response": response}
}

func toolResponse(id, name string, args map[string]any) map[string]any {
	return map[string]any{"tool_calls": []any{map[string]any{"id": id, "name": name, "args": args}}}
}

func writeHandConfig(t *testing.T, path, serverURL string, credential *store.Credential, workDir, tasksDir string) {
	t.Helper()
	content := fmt.Sprintf(`[server]
url = %s
token = %s
application_key = %s

[hand]
id = %s
work_dir = %s

[hand.permission]
allow_tools = ["write_file", "exec_command"]
deny_tools = []

[hand.limits]
max_output_size = 1048576

[hand.retry]
max_backoff = 1

[hand.tasks]
dir = %s
max_running = 2
max_runtime = "1m"
max_log_bytes = 1048576
retention = "1h"
max_retained = 20
`, strconv.Quote(serverURL), strconv.Quote(credential.Token), strconv.Quote(credential.ApplicationKey),
		strconv.Quote(credential.Label), strconv.Quote(workDir), strconv.Quote(tasksDir))
	writeFile(t, path, content, 0600)
}

func writeFaceConfig(t *testing.T, path, serverURL string, credential *store.FaceCredential, mode string) {
	t.Helper()
	content := fmt.Sprintf(`[server]
url = %s
token = %s
application_key = %s

[face]
id = %s
mode = %s
`, strconv.Quote(serverURL), strconv.Quote(credential.Token), strconv.Quote(credential.ApplicationKey),
		strconv.Quote(credential.Label), strconv.Quote(mode))
	writeFile(t, path, content, 0600)
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func awaitReady[T any](t *testing.T, process *testProcess, valid func(T) bool) T {
	t.Helper()
	line, err := process.stdout.next(faceMessageTimeout)
	if err != nil {
		t.Fatalf("wait for process ready message: %v\n%s", err, process.diagnostics())
	}
	value, err := protocol.StrictDecode[T]([]byte(line))
	if err != nil {
		t.Fatalf("decode process ready message: %v\nline: %s", err, line)
	}
	if valid == nil || !valid(value) {
		t.Fatalf("unexpected process ready payload: %+v", value)
	}
	return value
}

func createConversation(t *testing.T, face *faceClient) string {
	t.Helper()
	const requestID = "create-conversation"
	face.send(t, protocol.TypeFaceConversationCreate, protocol.FaceConversationCreate{RequestID: requestID, Name: "Process E2E"})
	awaitAccepted(t, face, requestID, protocol.FaceOperationConversationCreate)
	result := awaitResult(t, face, requestID)
	requireSuccessfulResult(t, result, protocol.FaceOperationConversationCreate)
	data, err := protocol.StrictDecode[protocol.ConversationCreateResult](result.Data)
	if err != nil || data.Conversation.ConversationID == "" {
		t.Fatalf("conversation create data = %+v, %v", data, err)
	}
	return data.Conversation.ConversationID
}

func requestSnapshot(t *testing.T, face *faceClient, requestID, conversationID string) protocol.ConversationSnapshot {
	t.Helper()
	face.send(t, protocol.TypeFaceConversationSnapshot, protocol.FaceConversationSnapshot{
		RequestID: requestID, ConversationID: conversationID,
	})
	awaitAccepted(t, face, requestID, protocol.FaceOperationConversationSnapshot)
	snapshot := awaitSnapshot(t, face, requestID)
	if snapshot.ConversationID != conversationID {
		t.Fatalf("snapshot conversation = %q, want %q", snapshot.ConversationID, conversationID)
	}
	return snapshot
}

func subscribe(t *testing.T, face *faceClient, requestID, conversationID string, eventTypes []protocol.FaceEventType) {
	t.Helper()
	face.send(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: requestID, ConversationIDs: []string{conversationID}, EventTypes: eventTypes,
	})
	awaitAccepted(t, face, requestID, protocol.FaceOperationSubscribe)
}

func runChat(t *testing.T, face *faceClient, requestID, conversationID, content string) protocol.FaceResult {
	t.Helper()
	face.send(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: requestID, ConversationID: conversationID, Content: content,
	})
	awaitAccepted(t, face, requestID, protocol.FaceOperationChat)
	return awaitResult(t, face, requestID)
}

func requireChatResult(t *testing.T, result protocol.FaceResult, content string) {
	t.Helper()
	if result.Status != protocol.FaceResultSucceeded || result.Content != content || len(result.Data) != 0 {
		t.Fatalf("Chat result = %+v, want content %q", result, content)
	}
}

func requireSnapshotContents(t *testing.T, snapshot protocol.ConversationSnapshot, contents ...string) {
	t.Helper()
	counts := make(map[string]int)
	for _, message := range snapshot.Messages {
		counts[message.Content]++
	}
	for _, content := range contents {
		if counts[content] != 1 {
			t.Fatalf("snapshot content %q count = %d", content, counts[content])
		}
	}
}

func awaitApproval(t *testing.T, face *faceClient, requestID, tool string) protocol.ApprovalRequest {
	t.Helper()
	event := awaitEvent(t, face, protocol.FaceEventApprovalRequested, func(event protocol.FaceEvent) bool {
		return event.RequestID == requestID
	})
	request := decodeEventData[protocol.ApprovalRequestedEventData](t, event)
	if request.RequestID != requestID || request.RunID == "" || request.Tool != tool || request.ArgsDigest == "" {
		t.Fatalf("approval request = %+v", request)
	}
	return request
}

func forbiddenResolve(t *testing.T, face *faceClient, approvalID string) {
	t.Helper()
	const requestID = "forbidden-resolve"
	face.send(t, protocol.TypeFaceApprovalResolve, protocol.FaceApprovalResolve{
		RequestID: requestID, ApprovalID: approvalID, Decision: protocol.FaceApprovalAllowOnce,
	})
	faceError := awaitFaceError(t, face, requestID)
	if faceError.Code != protocol.FaceErrorForbidden || faceError.Retryable {
		t.Fatalf("forbidden approval response = %+v", faceError)
	}
}

func resolveApproval(t *testing.T, face *faceClient, requestID, approvalID string) {
	t.Helper()
	face.send(t, protocol.TypeFaceApprovalResolve, protocol.FaceApprovalResolve{
		RequestID: requestID, ApprovalID: approvalID, Decision: protocol.FaceApprovalAllowOnce,
		Reason: "approved by process E2E",
	})
	awaitAccepted(t, face, requestID, protocol.FaceOperationApprovalResolve)
	result := awaitResult(t, face, requestID)
	requireSuccessfulResult(t, result, protocol.FaceOperationApprovalResolve)
}

func awaitRunStatus(t *testing.T, face *faceClient, runID string, status protocol.RunStatus) protocol.RemoteRunChangedEventData {
	t.Helper()
	event := awaitEvent(t, face, protocol.FaceEventRemoteRunChanged, func(event protocol.FaceEvent) bool {
		data, err := protocol.StrictDecode[protocol.RemoteRunChangedEventData](event.Data)
		return err == nil && data.RunID == runID && data.Status == status
	})
	return decodeEventData[protocol.RemoteRunChangedEventData](t, event)
}

func awaitChatCompleted(t *testing.T, face *faceClient, requestID string) {
	t.Helper()
	event := awaitEvent(t, face, protocol.FaceEventChatCompleted, func(event protocol.FaceEvent) bool {
		return event.RequestID == requestID
	})
	data := decodeEventData[protocol.ChatCompletedEventData](t, event)
	if data.RequestID != requestID {
		t.Fatalf("completed Chat = %+v", data)
	}
}

func getRun(t *testing.T, face *faceClient, requestID, conversationID, runID string) protocol.RemoteRunSummary {
	t.Helper()
	face.send(t, protocol.TypeFaceRunGet, protocol.FaceRunGet{
		RequestID: requestID, ConversationID: conversationID, RunID: runID,
	})
	awaitAccepted(t, face, requestID, protocol.FaceOperationRunGet)
	result := awaitResult(t, face, requestID)
	requireSuccessfulResult(t, result, protocol.FaceOperationRunGet)
	data, err := protocol.StrictDecode[protocol.RunGetResult](result.Data)
	if err != nil {
		t.Fatal(err)
	}
	return data.Run
}

func cancelRun(t *testing.T, face *faceClient, requestID, conversationID, runID string) {
	t.Helper()
	face.send(t, protocol.TypeFaceRunCancel, protocol.FaceRunCancel{
		RequestID: requestID, ConversationID: conversationID, RunID: runID, Reason: "process E2E",
	})
	awaitAccepted(t, face, requestID, protocol.FaceOperationRunCancel)
	result := awaitResult(t, face, requestID)
	requireSuccessfulResult(t, result, protocol.FaceOperationRunCancel)
}

func awaitTaskStatus(t *testing.T, face *faceClient, taskID string, status protocol.TaskStatus) protocol.TaskSummary {
	t.Helper()
	event := awaitEvent(t, face, protocol.FaceEventTaskChanged, func(event protocol.FaceEvent) bool {
		data, err := protocol.StrictDecode[protocol.TaskSummary](event.Data)
		return err == nil && data.TaskID == taskID && data.Status == status
	})
	return decodeEventData[protocol.TaskSummary](t, event)
}

func reconcileTask(t *testing.T, face *faceClient, conversationID, taskID string) protocol.TaskSummary {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		requestID := fmt.Sprintf("reconcile-task-%d", attempt)
		face.send(t, protocol.TypeFaceTaskGet, protocol.FaceTaskGet{
			RequestID: requestID, ConversationID: conversationID, TaskID: taskID,
		})
		awaitAccepted(t, face, requestID, protocol.FaceOperationTaskGet)
		result := awaitResult(t, face, requestID)
		requireSuccessfulResult(t, result, protocol.FaceOperationTaskGet)
		data, err := protocol.StrictDecode[protocol.TaskGetResult](result.Data)
		if err != nil {
			t.Fatal(err)
		}
		if protocol.IsTerminalTaskStatus(data.Task.Status) {
			return data.Task
		}
	}
	t.Fatalf("task %s did not reach a terminal state", taskID)
	return protocol.TaskSummary{}
}

func findSnapshotTask(t *testing.T, snapshot protocol.ConversationSnapshot, taskID string) protocol.TaskSummary {
	t.Helper()
	for _, task := range snapshot.Tasks {
		if task.TaskID == taskID {
			return task
		}
	}
	t.Fatalf("task %s not found in snapshot", taskID)
	return protocol.TaskSummary{}
}

func awaitOutputContains(t *testing.T, process *testProcess, values ...string) {
	t.Helper()
	pending := make(map[string]struct{}, len(values))
	for _, value := range values {
		pending[value] = struct{}{}
	}
	deadline := time.Now().Add(faceMessageTimeout)
	for len(pending) > 0 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("%s output missing %v\n%s", process.name, pending, process.diagnostics())
		}
		line, err := process.stdout.next(remaining)
		if err != nil {
			t.Fatalf("read %s output: %v\n%s", process.name, err, process.diagnostics())
		}
		for value := range pending {
			if strings.Contains(line, value) {
				delete(pending, value)
			}
		}
	}
}

func auditProcessState(
	t *testing.T,
	fixture processFixture,
	conversationID string,
	snapshot protocol.ConversationSnapshot,
	results map[string]protocol.FaceResult,
	approvals map[string]protocol.ApprovalRequest,
	runViews map[string]protocol.RemoteRunSummary,
	taskView protocol.TaskSummary,
) {
	t.Helper()
	db, err := store.New(fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	messages, err := db.GetMessages(conversationID)
	if err != nil {
		t.Fatal(err)
	}
	messageCounts := make(map[string]int)
	for _, message := range messages {
		messageCounts[message.Content]++
	}
	for _, value := range []string{
		simplePrompt, simpleAnswer, markerPrompt, markerAnswer,
		cancelPrompt, cancelAnswer, taskPrompt, taskAnswer,
	} {
		if messageCounts[value] != 1 {
			t.Fatalf("SQLite message %q count = %d", value, messageCounts[value])
		}
	}
	if messageCounts["conflicting replay"] != 0 {
		t.Fatal("conflicting replay reached SQLite history")
	}
	for requestID, result := range results {
		if messageCounts[result.Content] != 1 || result.RequestID != requestID {
			t.Fatalf("Face result and SQLite disagree for %s: %+v", requestID, result)
		}
	}
	runs, err := db.ListRemoteRunsBySession(conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("SQLite remote runs = %+v", runs)
	}
	runsByRequest := make(map[string]store.RemoteRunRecord, len(runs))
	for _, run := range runs {
		runsByRequest[run.RequestID] = run
		if run.SessionID != conversationID || run.HandID != handID || run.ArgsDigest == "" || run.ApprovalSource != "face" {
			t.Fatalf("invalid persisted run = %+v", run)
		}
		auditText := strings.Join([]string{
			run.ArgsDigest, run.ApprovalSource, run.ApprovalMode, run.ApprovalReason,
			run.RejectCode, run.Error,
		}, " ")
		assertNoAuditSecrets(t, auditText)
	}
	requireStoredRun(t, runsByRequest[markerRequestID], approvals[markerRequestID], runViews[markerRequestID], "write_file", protocol.RunSucceeded)
	requireStoredRun(t, runsByRequest[cancelRequestID], approvals[cancelRequestID], runViews[cancelRequestID], "exec_command", protocol.RunCancelled)
	backgroundRun := runsByRequest[taskRequestID]
	requireStoredRun(t, backgroundRun, approvals[taskRequestID], runViews[taskRequestID], "exec_command", protocol.RunSucceeded)
	if backgroundRun.ID != taskView.TaskID {
		t.Fatalf("background start run = %+v, task = %+v", backgroundRun, taskView)
	}
	storedTask, err := db.GetRemoteTask(taskView.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if storedTask.TaskID != backgroundRun.ID || storedTask.SessionID != conversationID || storedTask.HandID != handID ||
		storedTask.Tool != taskView.Tool || storedTask.ArgsDigest != taskView.ArgsDigest || storedTask.Status != protocol.TaskSucceeded || storedTask.Stale {
		t.Fatalf("persisted task = %+v, formal task = %+v", storedTask, taskView)
	}
	snapshotTask := findSnapshotTask(t, snapshot, storedTask.TaskID)
	if snapshotTask.Status != storedTask.Status || snapshotTask.ArgsDigest != storedTask.ArgsDigest || snapshotTask.Stale != storedTask.Stale {
		t.Fatalf("snapshot and SQLite task disagree: %+v %+v", snapshotTask, storedTask)
	}
	actorID := strconv.FormatInt(fixture.credentials["face-c"].ID, 10)
	for requestID, requested := range approvals {
		audit, found, lookupErr := db.LookupApproval(requested.ApprovalID)
		if lookupErr != nil || !found {
			t.Fatalf("approval %s lookup = %+v, %t, %v", requestID, audit, found, lookupErr)
		}
		run := runsByRequest[requestID]
		if audit.Status != approval.StatusResolved || audit.Decision != protocol.FaceApprovalAllowOnce ||
			audit.Actor.ID != actorID || audit.Actor.Label != "face-c" || audit.Actor.Source != "face" ||
			audit.Request.RunID != run.ID || audit.Request.ArgsDigest != run.ArgsDigest || audit.Request.Tool != run.Tool {
			t.Fatalf("approval audit %s = %+v, run = %+v", requestID, audit, run)
		}
		auditText := strings.Join([]string{
			audit.Request.Reason, audit.Request.ArgsDigest, audit.ResolutionReason,
			audit.Actor.ID, audit.Actor.Label, audit.Actor.Source,
		}, " ")
		assertNoAuditSecrets(t, auditText)
	}
}

func assertNoAuditSecrets(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{"remote-marker.txt", "written by process e2e", "sleep 30", "release-background"} {
		if strings.Contains(value, secret) {
			t.Fatalf("audit fields leaked %q: %s", secret, value)
		}
	}
}

func requireStoredRun(
	t *testing.T,
	run store.RemoteRunRecord,
	requested protocol.ApprovalRequest,
	view protocol.RemoteRunSummary,
	tool string,
	status protocol.RunStatus,
) {
	t.Helper()
	if run.ID == "" || run.ID != requested.RunID || run.ID != view.RunID || run.Tool != tool ||
		run.Status != status || view.Status != status || run.RequestID != requested.RequestID ||
		run.ArgsDigest != requested.ArgsDigest {
		t.Fatalf("persisted run mismatch: run=%+v approval=%+v view=%+v", run, requested, view)
	}
}
