package hand

import (
	"os"
	"runtime"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// CollectHandInfo 收集 Hand 静态设备信息。WorkDir 始终返回进程当前目录。
func CollectHandInfo() *protocol.HandInfo {
	host, _ := os.Hostname()
	wd, _ := os.Getwd()
	return &protocol.HandInfo{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Hostname: host,
		WorkDir:  wd,
	}
}
