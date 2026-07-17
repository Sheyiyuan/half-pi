package protocol

// RunStatus 是 Mind 维护的远程执行生命周期状态。
type RunStatus string

const (
	RunCreated         RunStatus = "created"
	RunApproved        RunStatus = "approved"
	RunSent            RunStatus = "sent"
	RunAccepted        RunStatus = "accepted"
	RunRunning         RunStatus = "running"
	RunSucceeded       RunStatus = "succeeded"
	RunFailed          RunStatus = "failed"
	RunRejected        RunStatus = "rejected"
	RunCancelRequested RunStatus = "cancel_requested"
	RunCancelled       RunStatus = "cancelled"
	RunTimedOut        RunStatus = "timed_out"
	RunLost            RunStatus = "lost"
)

var runTransitions = map[RunStatus]map[RunStatus]bool{
	RunCreated:  {RunApproved: true, RunRejected: true},
	RunApproved: {RunSent: true, RunRejected: true},
	RunSent: {
		RunAccepted: true, RunRejected: true, RunCancelRequested: true,
		RunTimedOut: true, RunLost: true,
	},
	RunAccepted: {
		RunRunning: true, RunSucceeded: true, RunFailed: true,
		RunCancelRequested: true, RunTimedOut: true, RunLost: true,
	},
	RunRunning: {
		RunSucceeded: true, RunFailed: true, RunCancelRequested: true,
		RunTimedOut: true, RunLost: true,
	},
	RunCancelRequested: {
		RunSucceeded: true, RunFailed: true, RunCancelled: true,
		RunTimedOut: true, RunLost: true,
	},
}

// IsTerminalRunStatus 判断状态是否为终态。
func IsTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunSucceeded, RunFailed, RunRejected, RunCancelled, RunTimedOut, RunLost:
		return true
	default:
		return false
	}
}

// CanTransitionRun 判断一次 run 状态迁移是否合法。
func CanTransitionRun(from, to RunStatus) bool {
	return runTransitions[from][to]
}
