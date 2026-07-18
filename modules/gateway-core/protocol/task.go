package protocol

import "fmt"

const (
	// MaxTaskRuntimeMS 是后台任务可请求的最长运行时间（24 小时）。
	MaxTaskRuntimeMS int64 = 24 * 60 * 60 * 1000
	// MaxTaskLogResponseBytes 是单次任务日志响应可携带的最大字节数。
	MaxTaskLogResponseBytes = 64 << 10
)

// TaskStatus 是后台任务的生命周期状态。
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
	TaskTimedOut  TaskStatus = "timed_out"
	TaskLost      TaskStatus = "lost"
)

// TaskCancelStatus 是 Hand 对后台任务取消请求的处理结果。
type TaskCancelStatus string

const (
	TaskCancelCancelled   TaskCancelStatus = "cancelled"
	TaskCancelAlreadyDone TaskCancelStatus = "already_done"
	TaskCancelUnknownTask TaskCancelStatus = "unknown_task"
	TaskCancelFailed      TaskCancelStatus = "failed"
)

// TaskStatusReq 请求后台任务状态。
type TaskStatusReq struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
}

// TaskStatusResp 返回后台任务状态及生命周期时间。
type TaskStatusResp struct {
	ID         string     `json:"id"`
	TaskID     string     `json:"task_id"`
	Tool       string     `json:"tool"`
	Status     TaskStatus `json:"status"`
	CreatedAt  int64      `json:"created_at"`
	StartedAt  int64      `json:"started_at,omitempty"`
	FinishedAt int64      `json:"finished_at,omitempty"`
	LogBytes   int64      `json:"log_bytes"`
	Truncated  bool       `json:"truncated,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// TaskLogReq 从精确字节偏移读取后台任务日志。
type TaskLogReq struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Offset int64  `json:"offset"`
	Limit  int    `json:"limit"`
}

// TaskLogResp 返回从 Offset 开始的 base64 编码日志字节。
// encoding/json 会将 Data 的 []byte 表示编码为 base64 字符串。
type TaskLogResp struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Data       []byte `json:"data"`
	EOF        bool   `json:"eof"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// TaskCancel 请求 Hand 取消后台任务。
type TaskCancel struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

// TaskCancelResult 是 Hand 对后台任务取消请求的响应。
type TaskCancelResult struct {
	ID     string           `json:"id"`
	TaskID string           `json:"task_id"`
	Status TaskCancelStatus `json:"status"`
	Error  string           `json:"error,omitempty"`
}

// ValidateTaskStatusReq 校验任务状态请求。
func ValidateTaskStatusReq(msg TaskStatusReq) error {
	return validateTaskRequest(msg.ID, msg.TaskID)
}

// ValidateTaskStatusResp 校验任务状态响应。
func ValidateTaskStatusResp(msg TaskStatusResp) error {
	if err := validateTaskRequest(msg.ID, msg.TaskID); err != nil {
		return err
	}
	if msg.Tool == "" {
		return fmt.Errorf("tool is required")
	}
	if !validTaskStatus(msg.Status) {
		return fmt.Errorf("unknown task status %q", msg.Status)
	}
	if msg.CreatedAt <= 0 {
		return fmt.Errorf("created_at is required")
	}
	if msg.StartedAt < 0 || msg.FinishedAt < 0 {
		return fmt.Errorf("task timestamps must not be negative")
	}
	if msg.LogBytes < 0 {
		return fmt.Errorf("log_bytes must not be negative")
	}
	if msg.StartedAt > 0 && msg.StartedAt < msg.CreatedAt {
		return fmt.Errorf("started_at must not precede created_at")
	}
	if msg.FinishedAt > 0 && (msg.StartedAt == 0 || msg.FinishedAt < msg.StartedAt) {
		return fmt.Errorf("finished_at must not precede started_at")
	}
	if IsTerminalTaskStatus(msg.Status) != (msg.FinishedAt > 0) {
		return fmt.Errorf("finished_at must be set exactly for terminal status")
	}
	return nil
}

// ValidateTaskLogReq 校验任务日志请求和响应大小上限。
func ValidateTaskLogReq(msg TaskLogReq) error {
	if err := validateTaskRequest(msg.ID, msg.TaskID); err != nil {
		return err
	}
	if msg.Offset < 0 {
		return fmt.Errorf("offset must not be negative")
	}
	if msg.Limit <= 0 || msg.Limit > MaxTaskLogResponseBytes {
		return fmt.Errorf("limit must be between 1 and %d", MaxTaskLogResponseBytes)
	}
	return nil
}

// ValidateTaskLogResp 校验任务日志响应的精确偏移和大小上限。
func ValidateTaskLogResp(msg TaskLogResp) error {
	if err := validateTaskRequest(msg.ID, msg.TaskID); err != nil {
		return err
	}
	if msg.Offset < 0 {
		return fmt.Errorf("offset must not be negative")
	}
	if len(msg.Data) > MaxTaskLogResponseBytes {
		return fmt.Errorf("task log data exceeds %d bytes", MaxTaskLogResponseBytes)
	}
	if msg.NextOffset != msg.Offset+int64(len(msg.Data)) {
		return fmt.Errorf("next_offset must equal offset plus data length")
	}
	return nil
}

// ValidateTaskCancel 校验后台任务取消请求。
func ValidateTaskCancel(msg TaskCancel) error {
	return validateTaskRequest(msg.ID, msg.TaskID)
}

// ValidateTaskCancelResult 校验后台任务取消结果。
func ValidateTaskCancelResult(msg TaskCancelResult) error {
	if err := validateTaskRequest(msg.ID, msg.TaskID); err != nil {
		return err
	}
	switch msg.Status {
	case TaskCancelCancelled, TaskCancelAlreadyDone, TaskCancelUnknownTask, TaskCancelFailed:
		return nil
	default:
		return fmt.Errorf("unknown task cancel status %q", msg.Status)
	}
}

// IsTerminalTaskStatus 判断后台任务状态是否为终态。
func IsTerminalTaskStatus(status TaskStatus) bool {
	switch status {
	case TaskSucceeded, TaskFailed, TaskCancelled, TaskTimedOut, TaskLost:
		return true
	default:
		return false
	}
}

func validTaskStatus(status TaskStatus) bool {
	switch status {
	case TaskPending, TaskRunning, TaskSucceeded, TaskFailed, TaskCancelled, TaskTimedOut, TaskLost:
		return true
	default:
		return false
	}
}

func validateTaskID(taskID string) error {
	if taskID == "" {
		return fmt.Errorf("task_id is required")
	}
	return nil
}

func validateTaskRequest(id, taskID string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	return validateTaskID(taskID)
}
