package hand

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/config"
)

const (
	monitorCommandTimeout = 10 * time.Second
	monitorOutputLimit    = 64 << 10
)

// startMonitors 按配置启动所有后台监控 goroutine。
func (h *Hand) startMonitors(ctx context.Context) {
	if h.cfg == nil || len(h.cfg.Hand.Monitors) == 0 {
		return
	}
	for _, m := range h.cfg.Hand.Monitors {
		if m.Name == "" || m.Command == "" {
			continue
		}
		go h.runMonitor(ctx, m)
	}
}

func (h *Hand) runMonitor(ctx context.Context, m config.MonitorConfig) {
	interval := m.Interval
	if interval <= 0 {
		interval = 60
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.checkAndSend(ctx, m)
		}
	}
}

func (h *Hand) checkAndSend(parent context.Context, m config.MonitorConfig) {
	ctx, cancel := context.WithTimeout(parent, monitorCommandTimeout)
	defer cancel()

	limit := h.monitorOutputLimit()
	cmd := monitorShellCmd(ctx, m.Command)
	var stdout, stderr cappedBuffer
	stdout.max = limit
	stderr.max = limit
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if parent.Err() != nil {
		return
	}

	conditionValue := strings.TrimSpace(stdout.String())
	output := conditionValue
	if se := strings.TrimSpace(stderr.String()); se != "" {
		if output != "" {
			output += "\n" + se
		} else {
			output = se
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		err = ctx.Err()
		timeoutMsg := fmt.Sprintf("monitor command timed out after %s", monitorCommandTimeout)
		if output != "" {
			output += "\n" + timeoutMsg
		} else {
			output = timeoutMsg
		}
	}
	output, _ = truncateBytes(output, limit)

	// 命令失败（exit code ≠ 0）始终触发；有 condition 时按表达式决策
	triggered := err != nil
	if m.Condition != "" {
		// 命令失败时仍然触发（无视 condition 结果）
		triggered = triggered || evalCondition(m.Condition, conditionValue)
	}

	status := "ok"
	if triggered {
		status = "triggered"
	}

	env, envErr := protocol.NewEnvelope("", protocol.TypeHandEvent, protocol.HandEvent{
		Name:      m.Name,
		HandID:    h.conn.Session.LocalID,
		Status:    status,
		Output:    output,
		Timestamp: time.Now().Unix(),
	})
	if envErr != nil {
		fmt.Fprintf(os.Stderr, "monitor %s: create event: %v\n", m.Name, envErr)
		return
	}
	if err := h.conn.Send(*env); err != nil {
		fmt.Fprintf(os.Stderr, "monitor %s: send event: %v\n", m.Name, err)
	}
}

func (h *Hand) monitorOutputLimit() int64 {
	limit := h.maxOutputSize()
	if limit <= 0 || limit > monitorOutputLimit {
		return monitorOutputLimit
	}
	return limit
}

type cappedBuffer struct {
	buf       bytes.Buffer
	max       int64
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 {
		b.max = monitorOutputLimit
	}
	remaining := b.max - int64(b.buf.Len())
	if remaining > 0 {
		n := len(p)
		if int64(n) > remaining {
			n = int(remaining)
			b.truncated = true
		}
		_, _ = b.buf.Write(p[:n])
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		return s + "\n...(truncated)"
	}
	return s
}

func evalCondition(cond, value string) bool {
	op, threshold, ok := parseCondition(cond)
	if !ok {
		return false
	}

	v, errV := strconv.ParseFloat(value, 64)
	t, errT := strconv.ParseFloat(threshold, 64)
	if errV == nil && errT == nil {
		return compareFloat(op, v, t)
	}

	switch op {
	case "==":
		return value == threshold
	case "!=":
		return value != threshold
	default:
		return false
	}
}

func parseCondition(s string) (op, threshold string, ok bool) {
	s = strings.TrimSpace(s)

	for _, op := range []string{">=", "<=", "!=", "=="} {
		if strings.HasPrefix(s, op) {
			return op, strings.TrimSpace(s[len(op):]), true
		}
	}
	for _, op := range []string{">", "<"} {
		if strings.HasPrefix(s, op) {
			return op, strings.TrimSpace(s[len(op):]), true
		}
	}
	return "", "", false
}

func compareFloat(op string, v, t float64) bool {
	switch op {
	case ">":
		return v > t
	case "<":
		return v < t
	case ">=":
		return v >= t
	case "<=":
		return v <= t
	case "==":
		return v == t
	case "!=":
		return v != t
	default:
		return false
	}
}
