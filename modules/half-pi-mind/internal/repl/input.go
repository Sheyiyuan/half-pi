package repl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/chzyer/readline"
	"github.com/mattn/go-isatty"
)

type inputLine struct {
	text string
	ok   bool
}

// InputReader 提供 REPL 和审批共用的终端输入。
//
// TTY 使用 readline 实现行编辑；非 TTY 保持按行读取，保证管道和脚本兼容。
type InputReader struct {
	lines     <-chan inputLine
	requests  chan inputRequest
	done      chan struct{}
	closeOnce sync.Once
	editor    *readline.Instance
	stdout    io.Writer
	stderr    io.Writer
	historyMu sync.Mutex
	lastLine  string
	activeMu  sync.Mutex
	active    *activeInput
	changed   chan struct{}
}

// inputReader 保留内部测试和旧调用点使用的小写别名。
type inputReader = InputReader

type inputRequest struct {
	input *activeInput
}

type activeInput struct {
	result  chan inputLine
	claimed bool
}

// NewInput 根据 stdin 是否连接到终端创建输入编辑器。
func NewInput(stdin *os.File, stdout, stderr io.Writer) (*InputReader, error) {
	if stdin == nil {
		return nil, fmt.Errorf("stdin is nil")
	}
	if isatty.IsTerminal(stdin.Fd()) || isatty.IsCygwinTerminal(stdin.Fd()) {
		return newTTYInput(stdin, stdout, stderr)
	}
	return newInputReader(bufio.NewScanner(stdin), stdout, stderr), nil
}

func newInputReader(scanner *bufio.Scanner, outputs ...io.Writer) *InputReader {
	done := make(chan struct{})
	var stdout, stderr io.Writer = os.Stdout, os.Stderr
	if len(outputs) > 0 && outputs[0] != nil {
		stdout = outputs[0]
	}
	if len(outputs) > 1 && outputs[1] != nil {
		stderr = outputs[1]
	}
	lines := make(chan inputLine)
	go func() {
		for scanner.Scan() {
			select {
			case lines <- inputLine{text: scanner.Text(), ok: true}:
			case <-done:
				return
			}
		}
		select {
		case lines <- inputLine{}:
		case <-done:
		}
		close(lines)
	}()
	return &InputReader{lines: lines, done: done, stdout: stdout, stderr: stderr}
}

func newTTYInput(stdin *os.File, stdout, stderr io.Writer) (*InputReader, error) {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	editor, err := readline.NewEx(&readline.Config{
		Prompt:                 "> ",
		HistoryLimit:           500,
		DisableAutoSaveHistory: true,
		InterruptPrompt:        "\n",
		EOFPrompt:              "\n",
		Stdin:                  stdin,
		Stdout:                 stdout,
		Stderr:                 stderr,
		FuncIsTerminal:         func() bool { return true },
	})
	if err != nil {
		return nil, fmt.Errorf("initialize terminal input: %w", err)
	}
	done := make(chan struct{})
	reader := &InputReader{
		requests: make(chan inputRequest),
		done:     done,
		editor:   editor,
		stdout:   editor.Stdout(),
		stderr:   editor.Stderr(),
		changed:  make(chan struct{}),
	}
	go reader.serveTTY()
	return reader, nil
}

func (r *InputReader) serveTTY() {
	for {
		select {
		case request := <-r.requests:
			request.input.result <- r.readTTYLine()
		case <-r.done:
			return
		}
	}
}

func (r *InputReader) readTTYLine() inputLine {
	for {
		line, err := r.editor.Readline()
		if err == readline.ErrInterrupt && line != "" {
			// Ctrl+C clears a non-empty line and starts a fresh prompt.
			continue
		}
		if err != nil {
			return inputLine{}
		}
		return inputLine{text: line, ok: true}
	}
}

func (r *InputReader) remember(line string) {
	if r.editor == nil || strings.TrimSpace(line) == "" {
		return
	}
	r.historyMu.Lock()
	defer r.historyMu.Unlock()
	if line == r.lastLine {
		return
	}
	if err := r.editor.SaveHistory(line); err == nil {
		r.lastLine = line
	}
}

// Read 读取一条已提交的输入。
func (r *InputReader) Read(ctx context.Context) (string, bool) {
	return r.ReadLine(ctx, "", false)
}

// ReadLine 使用指定提示符读取一行，可选择是否将输入加入历史。
func (r *InputReader) ReadLine(ctx context.Context, prompt string, saveHistory bool) (string, bool) {
	if r.editor != nil {
		active, start, ok := r.claimActive(ctx, prompt)
		if !ok {
			return "", false
		}
		if start {
			select {
			case r.requests <- inputRequest{input: active}:
			case <-ctx.Done():
				r.finishActive(active)
				return "", false
			case <-r.done:
				r.finishActive(active)
				return "", false
			}
		}
		select {
		case line := <-active.result:
			r.finishActive(active)
			if line.ok && saveHistory {
				r.remember(line.text)
			}
			return line.text, line.ok
		case <-ctx.Done():
			r.releaseActive(active)
			return "", false
		case <-r.done:
			r.releaseActive(active)
			return "", false
		}
	}
	if prompt != "" {
		_, _ = fmt.Fprint(r.stdout, prompt)
	}
	select {
	case line, open := <-r.lines:
		return line.text, open && line.ok
	case <-ctx.Done():
		return "", false
	}
}

func (r *InputReader) claimActive(ctx context.Context, prompt string) (*activeInput, bool, bool) {
	for {
		r.activeMu.Lock()
		active := r.active
		start := active == nil
		if start {
			active = &activeInput{result: make(chan inputLine, 1)}
			r.active = active
		}
		if !active.claimed {
			active.claimed = true
			r.editor.Operation.SetPrompt(prompt)
			if !start {
				r.editor.Refresh()
			}
			r.activeMu.Unlock()
			return active, start, true
		}
		changed := r.changed
		r.activeMu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, false, false
		case <-r.done:
			return nil, false, false
		}
	}
}

func (r *InputReader) releaseActive(active *activeInput) {
	r.activeMu.Lock()
	if r.active == active {
		active.claimed = false
		r.notifyActiveChanged()
	}
	r.activeMu.Unlock()
}

func (r *InputReader) finishActive(active *activeInput) {
	r.activeMu.Lock()
	if r.active == active {
		r.active = nil
		r.notifyActiveChanged()
	}
	r.activeMu.Unlock()
}

func (r *InputReader) notifyActiveChanged() {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *InputReader) read(ctx context.Context) (string, bool) { return r.Read(ctx) }

// Close 关闭输入编辑器并恢复终端状态。
func (r *InputReader) Close() error {
	r.closeOnce.Do(func() {
		close(r.done)
		if r.editor != nil {
			_ = r.editor.Close()
		}
	})
	return nil
}

// Stdout 返回 REPL 的标准输出 writer。
func (r *InputReader) Stdout() io.Writer { return r.stdout }

// Stderr 返回可在编辑输入时安全重绘的终端 writer。
func (r *InputReader) Stderr() io.Writer { return r.stderr }
