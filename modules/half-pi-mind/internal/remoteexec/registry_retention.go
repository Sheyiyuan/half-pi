package remoteexec

import (
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func (r *Registry) pruneRunsLocked(now time.Time) {
	terminalCount := 0
	for id, run := range r.runs {
		if !protocol.IsTerminalRunStatus(run.Status) || run.waiters > 0 {
			continue
		}
		if now.Sub(run.FinishedAt) >= terminalRunRetention {
			delete(r.runs, id)
			continue
		}
		terminalCount++
	}
	for terminalCount >= maxTerminalRuns {
		var oldestID string
		var oldest time.Time
		for id, run := range r.runs {
			if !protocol.IsTerminalRunStatus(run.Status) || run.waiters > 0 {
				continue
			}
			if oldestID == "" || run.FinishedAt.Before(oldest) {
				oldestID, oldest = id, run.FinishedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(r.runs, oldestID)
		terminalCount--
	}
}
