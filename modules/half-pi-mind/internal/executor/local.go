package executor

import (
	"context"
	"fmt"
)

// LocalExecutor runs commands on the local machine.
type LocalExecutor struct{}

// NewLocal creates a new local executor.
func NewLocal() *LocalExecutor {
	return &LocalExecutor{}
}

// Exec runs a command locally.
func (e *LocalExecutor) Exec(ctx context.Context, req *ExecRequest) (*ExecResult, error) {
	return nil, fmt.Errorf("not implemented")
}
