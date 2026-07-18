package llm

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const (
	maxSSELineBytes      = 1 << 20
	maxSSEEventBytes     = 1 << 20
	maxProviderErrorBody = 64 << 10
	maxStreamToolArgs    = 1 << 20
)

type sseEvent struct {
	Type string
	Data string
}

func decodeSSE(reader io.Reader, handle func(sseEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxSSELineBytes)
	var eventType string
	var data []string
	eventBytes := 0
	flush := func() error {
		if len(data) == 0 {
			eventType = ""
			eventBytes = 0
			return nil
		}
		event := sseEvent{Type: eventType, Data: strings.Join(data, "\n")}
		eventType = ""
		data = data[:0]
		eventBytes = 0
		return handle(event)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			eventType = value
		case "data":
			eventBytes += len(value)
			if len(data) > 0 {
				eventBytes++
			}
			if eventBytes > maxSSEEventBytes {
				return fmt.Errorf("SSE event exceeds %d bytes", maxSSEEventBytes)
			}
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read SSE stream: %w", err)
	}
	return flush()
}

func readProviderError(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, maxProviderErrorBody+1))
	if err != nil {
		return "unreadable response body"
	}
	if len(data) > maxProviderErrorBody {
		return "response body exceeded limit"
	}
	return strings.TrimSpace(string(data))
}
