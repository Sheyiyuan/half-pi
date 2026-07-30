//go:build !windows

package repl

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

const ttyInputHelperEnv = "HALF_PI_REPL_INPUT_HELPER"

func TestInputReaderTTYEditing(t *testing.T) {
	if os.Getenv(ttyInputHelperEnv) == "1" {
		runTTYInputHelper()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestInputReaderTTYEditing$")
	cmd.Env = append(os.Environ(), ttyInputHelperEnv+"=1", "TERM=xterm-256color")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 100})
	if err != nil {
		t.Fatalf("start helper in PTY: %v", err)
	}
	defer terminal.Close()
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()
	output := &ptyOutput{terminal: terminal, reader: bufio.NewReader(terminal)}

	output.waitFor(t, "> ")
	writePTY(t, terminal, "中文X\x7f")
	output.waitFor(t, "ASYNC EVENT")
	writePTY(t, terminal, "\r")
	output.waitFor(t, acceptedLine("中文"))
	output.waitFor(t, "> ")

	writePTY(t, terminal, "ac\x1b[Db\x1b[Cd\r")
	output.waitFor(t, acceptedLine("abcd"))
	output.waitFor(t, "> ")

	writePTY(t, terminal, "\x1b[A\r")
	output.waitFor(t, acceptedLine("abcd"))
	output.waitFor(t, "> ")
	writePTY(t, terminal, "\x1b[A\x1b[A\r")
	output.waitFor(t, acceptedLine("中文"))
	output.waitFor(t, "> ")
	writePTY(t, terminal, "draft\x1b[A\x1b[B\r")
	output.waitFor(t, acceptedLine("draft"))
	output.waitFor(t, "> ")

	writePTY(t, terminal, "bc\x01a\x05d\r")
	output.waitFor(t, acceptedLine("abcd"))
	output.waitFor(t, "> ")
	writePTY(t, terminal, "hello world\x17gopher\r")
	output.waitFor(t, acceptedLine("hello gopher"))
	output.waitFor(t, "> ")
	writePTY(t, terminal, "discard\x15kept\r")
	output.waitFor(t, acceptedLine("kept"))
	output.waitFor(t, "> ")
	writePTY(t, terminal, "tail\x01\x0bhead\r")
	output.waitFor(t, acceptedLine("head"))
	output.waitFor(t, "> ")

	writePTY(t, terminal, "aXb\x1b[H\x1b[C\x1b[3~\r")
	output.waitFor(t, acceptedLine("ab"))
	output.waitFor(t, "> ")
	writePTY(t, terminal, "aXb\x1b[H\x1b[C\x04\r")
	output.waitFor(t, acceptedLine("ab"))
	output.waitFor(t, "> ")
	output.waitFor(t, "CANCELED")
	output.waitFor(t, "> ")
	writePTY(t, terminal, "discarded\x03")
	output.waitFor(t, "> ")
	writePTY(t, terminal, "kept after interrupt\r")
	output.waitFor(t, acceptedLine("kept after interrupt"))
	output.waitFor(t, "> ")

	writePTY(t, terminal, "\x03")
	output.waitFor(t, "CLOSED")
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper exited: %v\n%s", err, output.seen.String())
	}
}

func runTTYInputHelper() {
	input, err := NewInput(os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Printf("HELPER ERROR: %v\n", err)
		return
	}
	defer input.Close()
	for index := 0; ; index++ {
		if index == 0 {
			go func() {
				time.Sleep(500 * time.Millisecond)
				_, _ = fmt.Fprintln(input.Stderr(), "ASYNC EVENT")
			}()
		}
		ctx := context.Background()
		if index == 11 {
			cancelCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			line, ok := input.ReadLine(cancelCtx, "> ", true)
			cancel()
			if line != "" || ok {
				fmt.Printf("UNEXPECTED CANCEL RESULT:%q,%t\n", line, ok)
				return
			}
			fmt.Println("CANCELED")
			continue
		}
		line, ok := input.ReadLine(ctx, "> ", true)
		if !ok {
			fmt.Println("CLOSED")
			return
		}
		fmt.Println(acceptedLine(line))
	}
}

type ptyOutput struct {
	terminal *os.File
	reader   *bufio.Reader
	seen     strings.Builder
}

func (o *ptyOutput) waitFor(t *testing.T, want string) {
	t.Helper()
	if err := o.terminal.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set PTY deadline: %v", err)
	}
	for !strings.Contains(o.seen.String(), want) {
		value, err := o.reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				t.Fatalf("PTY closed before %q appeared: %q", want, o.seen.String())
			}
			t.Fatalf("read PTY waiting for %q: %v\n%s", want, err, o.seen.String())
		}
		o.seen.WriteByte(value)
	}
	text := o.seen.String()
	index := strings.Index(text, want) + len(want)
	o.seen.Reset()
	o.seen.WriteString(text[index:])
}

func writePTY(t *testing.T, terminal *os.File, value string) {
	t.Helper()
	if _, err := io.WriteString(terminal, value); err != nil {
		t.Fatalf("write PTY input: %v", err)
	}
}

func acceptedLine(line string) string {
	return "ACCEPT:" + base64.StdEncoding.EncodeToString([]byte(line))
}
