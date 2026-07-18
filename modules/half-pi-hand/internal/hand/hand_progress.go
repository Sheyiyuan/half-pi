package hand

import (
	"sync"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

const progressQueueSize = 16

type progressPump struct {
	hand  *Hand
	runID string
	limit int64
	queue chan executor.Progress
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func newProgressPump(hand *Hand, runID string, limit int64) *progressPump {
	pump := &progressPump{
		hand: hand, runID: runID, limit: limit,
		queue: make(chan executor.Progress, progressQueueSize), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go pump.run()
	return pump
}

func (p *progressPump) report(progress executor.Progress) {
	select {
	case <-p.stop:
		return
	case p.queue <- progress:
	default:
	}
}

// close discards queued progress and waits for an already-started Send to return.
// The producer is bounded and nonblocking, but a WebSocket frame already being
// written can delay the result until the transport write timeout.
func (p *progressPump) close() {
	p.once.Do(func() { close(p.stop) })
	<-p.done
}

func (p *progressPump) run() {
	defer close(p.done)
	var seq int64
	var byteCount int64
	var eventCount int
	for {
		select {
		case <-p.stop:
			return
		case progress := <-p.queue:
			select {
			case <-p.stop:
				return
			default:
			}
			kind := protocol.ProgressKind(progress.Kind)
			if (kind != protocol.ProgressStdout && kind != protocol.ProgressStderr) || progress.Data == "" {
				continue
			}
			remaining := p.limit - byteCount
			if remaining <= 0 || eventCount >= protocol.MaxRPCProgressEvents {
				continue
			}
			data, _ := truncateUTF8(progress.Data, min(remaining, int64(protocol.MaxRPCProgressChunkBytes)))
			if data == "" {
				continue
			}
			seq++
			msg := protocol.RPCProgress{RunID: p.runID, Seq: seq, Kind: kind, Data: data}
			if err := p.hand.sendRPCMessage(protocol.TypeRPCProgress, msg); err != nil {
				return
			}
			byteCount += int64(len(data))
			eventCount++
		}
	}
}

func truncateUTF8(s string, max int64) (string, bool) {
	if max <= 0 {
		return "", s != ""
	}
	if int64(len(s)) <= max {
		return s, false
	}
	end := int(max)
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end], true
}
