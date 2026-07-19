package adminipc

import (
	"strconv"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
)

func intString(v int64) string {
	return strconv.FormatInt(v, 10)
}

func managementError(code, message string) error {
	return &management.Error{Code: code, Message: message}
}
