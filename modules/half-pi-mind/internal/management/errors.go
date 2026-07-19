package management

import "fmt"

// Error 是管理面的稳定错误。
type Error struct {
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Cause }

func errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func wrap(code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Cause: err}
}
