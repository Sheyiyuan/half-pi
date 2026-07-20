//go:build !windows

package e2e

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

const processWaitTimeout = 15 * time.Second

type testBinaries struct {
	mind string
	hand string
	face string
}

type lineOutput struct {
	lines chan string

	mu      sync.Mutex
	history []string
	scanErr error
}

func newLineOutput(reader io.Reader) *lineOutput {
	output := &lineOutput{lines: make(chan string, 2048)}
	go func() {
		defer close(output.lines)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 2<<20)
		for scanner.Scan() {
			line := scanner.Text()
			output.mu.Lock()
			output.history = append(output.history, line)
			output.mu.Unlock()
			output.lines <- line
		}
		output.mu.Lock()
		output.scanErr = scanner.Err()
		output.mu.Unlock()
	}()
	return output
}

func (o *lineOutput) next(timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line, ok := <-o.lines:
		if ok {
			return line, nil
		}
		o.mu.Lock()
		err := o.scanErr
		o.mu.Unlock()
		if err != nil {
			return "", err
		}
		return "", io.EOF
	case <-timer.C:
		return "", fmt.Errorf("timed out waiting for process output")
	}
}

func (o *lineOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.history, "\n")
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testProcess struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *lineOutput
	stderr lockedBuffer
	done   chan struct{}

	waitMu  sync.Mutex
	waitErr error
}

func startTestProcess(t *testing.T, name, binary, dir, home string, args ...string) *testProcess {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = environmentWithHome(home)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("%s stdin: %v", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("%s stdout: %v", name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("%s stderr: %v", name, err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	process := &testProcess{
		name: name, cmd: cmd, stdin: stdin, stdout: newLineOutput(stdout), done: make(chan struct{}),
	}
	go func() {
		_, _ = io.Copy(&process.stderr, stderr)
	}()
	go func() {
		err := cmd.Wait()
		_ = process.stdin.Close()
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	}()
	t.Cleanup(func() { process.forceStop() })
	return process
}

func startTTYTestProcess(t *testing.T, name, binary, dir, home string, args ...string) *testProcess {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = append(environmentWithHome(home), "TERM=xterm-256color", "COLORTERM=truecolor")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Fatalf("start %s in PTY: %v", name, err)
	}
	process := &testProcess{
		name: name, cmd: cmd, stdin: terminal, stdout: newLineOutput(terminal), done: make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		_ = terminal.Close()
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	}()
	t.Cleanup(func() { process.forceStop() })
	return process
}

func (p *testProcess) wait(timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		p.waitMu.Lock()
		defer p.waitMu.Unlock()
		return p.waitErr
	case <-timer.C:
		return fmt.Errorf("%s did not exit within %s", p.name, timeout)
	}
}

func (p *testProcess) closeInputAndWait() error {
	if err := p.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("close %s stdin: %w", p.name, err)
	}
	return p.wait(processWaitTimeout)
}

func (p *testProcess) interruptAndWait() error {
	select {
	case <-p.done:
		return p.wait(time.Second)
	default:
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGINT); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("interrupt %s: %w", p.name, err)
	}
	return p.wait(processWaitTimeout)
}

func (p *testProcess) forceStop() {
	select {
	case <-p.done:
		return
	default:
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.wait(processWaitTimeout)
}

func (p *testProcess) diagnostics() string {
	return fmt.Sprintf("stdout:\n%s\nstderr:\n%s", p.stdout.String(), p.stderr.String())
}

func environmentWithHome(home string) []string {
	environment := os.Environ()
	result := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		if !strings.HasPrefix(value, "HOME=") {
			result = append(result, value)
		}
	}
	return append(result, "HOME="+home)
}

func buildTestBinaries(t *testing.T, destination string) testBinaries {
	t.Helper()
	root := repositoryRoot(t)
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatalf("create binary directory: %v", err)
	}
	specs := []struct {
		name   string
		pkg    string
		target *string
	}{
		{name: "mind", pkg: "./modules/half-pi-mind/cmd/half-pi-mind"},
		{name: "hand", pkg: "./modules/half-pi-hand/cmd/half-pi-hand"},
		{name: "face", pkg: "./modules/half-pi-face/cmd/half-pi-face"},
	}
	var binaries testBinaries
	specs[0].target = &binaries.mind
	specs[1].target = &binaries.hand
	specs[2].target = &binaries.face
	for _, spec := range specs {
		output := filepath.Join(destination, spec.name)
		command := exec.Command("go", "build", "-race", "-o", output, spec.pkg)
		command.Dir = root
		combined, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("build %s: %v\n%s", spec.name, err, combined)
		}
		*spec.target = output
	}
	return binaries
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate E2E source file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	return root
}
