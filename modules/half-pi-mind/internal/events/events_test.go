package events

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewEvent(t *testing.T) {
	e := New("sess-1", "agentcore", LevelInfo, TypeSystem, "系统启动")

	if e.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", e.SessionID, "sess-1")
	}
	if e.Source != "agentcore" {
		t.Errorf("Source = %q, want %q", e.Source, "agentcore")
	}
	if e.Level != LevelInfo {
		t.Errorf("Level = %q, want %q", e.Level, LevelInfo)
	}
	if e.Type != TypeSystem {
		t.Errorf("Type = %q, want %q", e.Type, TypeSystem)
	}
	if e.Message != "系统启动" {
		t.Errorf("Message = %q, want %q", e.Message, "系统启动")
	}
	if e.ID == "" {
		t.Error("ID 不应为空")
	}
	if e.Timestamp.IsZero() {
		t.Error("Timestamp 不应为零值")
	}
}

func TestEventWithData(t *testing.T) {
	e := New("sess-1", "tool", LevelDebug, TypeToolCall, "list_dir")
	e2 := e.WithData(map[string]any{"path": "/tmp"})

	// 原始事件不受影响
	if e.Data != nil {
		t.Error("原始事件的 Data 应为 nil")
	}
	// 新事件带数据
	if e2.Data == nil {
		t.Error("WithData 返回的事件 Data 不应为 nil")
	}
	data, ok := e2.Data.(map[string]any)
	if !ok || data["path"] != "/tmp" {
		t.Errorf("Data.path = %v, want /tmp", data["path"])
	}
}

func TestEventIDUniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		e := New("sess-1", "test", LevelDebug, TypeSystem, "")
		if ids[e.ID] {
			t.Fatalf("重复 ID: %s", e.ID)
		}
		ids[e.ID] = true
	}
}

func TestBusSubscribeAndPublish(t *testing.T) {
	bus := NewEventBus()
	var mu sync.Mutex
	var got []Event

	bus.Subscribe(&testWriter{fn: func(e Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	}})

	e1 := New("s-1", "test", LevelInfo, TypeSystem, "事件A")
	e2 := New("s-1", "test", LevelDebug, TypeToolCall, "工具调用")

	bus.Publish(e1)
	bus.Publish(e2)

	// 异步写入，等一会
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 2 })

	if len(got) != 2 {
		t.Fatalf("收到 %d 个事件，期望 2", len(got))
	}
	if got[0].Message != "事件A" {
		t.Errorf("第1个事件 message = %q", got[0].Message)
	}
	if got[1].Type != TypeToolCall {
		t.Errorf("第2个事件 type = %q", got[1].Type)
	}
}

func TestBusMultipleSubscribers(t *testing.T) {
	bus := NewEventBus()
	var c1, c2 int
	var mu1, mu2 sync.Mutex

	bus.Subscribe(&testWriter{fn: func(e Event) { mu1.Lock(); c1++; mu1.Unlock() }})
	bus.Subscribe(&testWriter{fn: func(e Event) { mu2.Lock(); c2++; mu2.Unlock() }})

	bus.Publish(New("s-1", "test", LevelInfo, TypeSystem, "x"))
	bus.Publish(New("s-1", "test", LevelInfo, TypeSystem, "y"))

	waitFor(t, func() bool { mu1.Lock(); defer mu1.Unlock(); return c1 >= 2 })
	waitFor(t, func() bool { mu2.Lock(); defer mu2.Unlock(); return c2 >= 2 })

	if c1 != 2 || c2 != 2 {
		t.Fatalf("c1=%d c2=%d，期望均为 2", c1, c2)
	}
}

func TestConsoleWriterToolCall(t *testing.T) {
	// 捕获 stderr 输出
	stderr := captureStderr(t, func() {
		w := NewConsoleWriter()
		e := New("s-1", "agentcore", LevelDebug, TypeToolCall, `list_dir({"path":"."})`)
		w.WriteEvent(e)
	})

	if !strings.Contains(stderr, "── [TOOL] ") {
		t.Errorf("Console 输出缺少 [TOOL] 前缀: %q", stderr)
	}
	if !strings.Contains(stderr, `list_dir({`) {
		t.Errorf("Console 输出缺少工具名: %q", stderr)
	}
}

func TestConsoleWriterEmptyMessage(t *testing.T) {
	stderr := captureStderr(t, func() {
		w := NewConsoleWriter()
		e := New("s-1", "test", LevelDebug, TypeToolResult, "")
		w.WriteEvent(e)
	})
	if stderr != "" {
		t.Errorf("空消息不应输出: %q", stderr)
	}
}

func TestFileWriter(t *testing.T) {
	f, err := os.CreateTemp("", "events-test-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	w, err := NewFileWriter(path)
	if err != nil {
		t.Fatal(err)
	}

	e1 := New("s-1", "agentcore", LevelInfo, TypeSystem, "启动").WithData(map[string]any{"pid": 123})
	e2 := New("s-1", "tool", LevelDebug, TypeToolCall, "ls")

	w.WriteEvent(e1)
	w.WriteEvent(e2)
	w.Close()

	// 读回并验证
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("文件应有 2 行，实际 %d", len(lines))
	}

	var decoded Event
	json.Unmarshal([]byte(lines[0]), &decoded)
	if decoded.Message != "启动" {
		t.Errorf("第1行 message = %q", decoded.Message)
	}
	if decoded.Data == nil {
		t.Error("第1行应包含 Data")
	}

	json.Unmarshal([]byte(lines[1]), &decoded)
	if decoded.Type != TypeToolCall {
		t.Errorf("第2行 type = %q", decoded.Type)
	}
}

func TestRace(t *testing.T) {
	bus := NewEventBus()
	bus.Subscribe(NewConsoleWriter())

	f, _ := os.CreateTemp("", "events-race-*.jsonl")
	defer os.Remove(f.Name())
	f.Close()
	fw, _ := NewFileWriter(f.Name())
	bus.Subscribe(fw)
	defer fw.Close()

	var wg sync.WaitGroup

	// 并发发布者
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				bus.Publish(New("race-session", "tester", LevelDebug, TypeToolCall, "并发事件"))
			}
		}(i)
	}

	// 并发订阅者
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := NewConsoleWriter()
			bus.Subscribe(w)
		}()
	}

	wg.Wait()
	bus.Close()
}

// ── 测试辅助 ──

type testWriter struct {
	fn func(Event)
}

func (w *testWriter) WriteEvent(e Event) error {
	w.fn(e)
	return nil
}
func (w *testWriter) Close() error { return nil }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("等待超时")
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	var buf strings.Builder
	// 用 os.ReadFile 的代替方案
	b := make([]byte, 4096)
	for {
		n, err := r.Read(b)
		buf.Write(b[:n])
		if err != nil {
			break
		}
	}
	r.Close()
	return buf.String()
}
