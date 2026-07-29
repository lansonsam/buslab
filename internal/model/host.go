package model

import "fmt"

// SerialBackend 表示串口设备的来源。
type SerialBackend string

const (
	SerialBackendNone    SerialBackend = "none"
	SerialBackendTTY0TTY SerialBackend = "tty0tty"
	SerialBackendPTY     SerialBackend = "pty"
)

func (b SerialBackend) Label() string {
	switch b {
	case SerialBackendTTY0TTY:
		return "tty0tty（外部工具可见）"
	case SerialBackendPTY:
		return "pty（仅本进程可见）"
	}
	return "不可用"
}

// HostReport 描述宿主机能力，用于状态栏与功能开关。
type HostReport struct {
	OS            string
	Supported     bool
	Root          bool
	NetAdmin      bool
	VCanModule    bool
	IPCommand     bool
	SerialBackend SerialBackend
	FreeTTYPairs  int
	Warnings      []string
	Errors        []string
}

func (r HostReport) CANAvailable() bool {
	return r.Supported && r.IPCommand && (r.NetAdmin || r.Root)
}

func (r HostReport) SerialAvailable() bool {
	return r.Supported && r.SerialBackend != SerialBackendNone
}

func (r *HostReport) Warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

func (r *HostReport) Fail(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func (r HostReport) StatusLine() string {
	can := "CAN 不可用"
	if r.CANAvailable() {
		can = "CAN 就绪"
		if !r.VCanModule {
			can = "CAN 待加载 vcan"
		}
	}
	return fmt.Sprintf("%s · 串口后端：%s · 权限：%s", can, r.SerialBackend.Label(), r.privilegeLabel())
}

func (r HostReport) privilegeLabel() string {
	switch {
	case r.Root:
		return "root"
	case r.NetAdmin:
		return "CAP_NET_ADMIN"
	default:
		return "普通用户"
	}
}
