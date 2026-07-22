package lifecycle

// Outcome 是生命周期操作的结果。
type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeTimedOut  Outcome = "timed_out"
	OutcomePanicked  Outcome = "panicked"
	OutcomeDenied    Outcome = "denied"
	OutcomeBlocked   Outcome = "blocked"
)

// IsTerminal 判断 outcome 是否代表不可逆的终态。
func (o Outcome) IsTerminal() bool {
	switch o {
	case OutcomeSucceeded, OutcomeFailed, OutcomeCancelled, OutcomeTimedOut, OutcomePanicked, OutcomeDenied, OutcomeBlocked:
		return true
	default:
		return false
	}
}
