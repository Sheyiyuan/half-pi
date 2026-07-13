// Package executor defines the execution interface shared by
// local and remote Hand executors.
package executor

import "context"

// ExecRequest describes a command to execute.
type ExecRequest struct {
	Command   string
	SessionID string
	Timeout   int // seconds
}

// ExecResult holds the outcome of an execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Executor runs commands on a target.
type Executor interface {
	Exec(ctx context.Context, req *ExecRequest) (*ExecResult, error)
}
