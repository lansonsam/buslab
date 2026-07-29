//go:build linux

package host

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lansonsam/buslab/internal/model"
)

const capNetAdmin = 12

func platformName() string { return "linux" }

func probePlatform(ctx context.Context, h *Host, r *model.HostReport) {
	r.Supported = true
	r.Root = os.Geteuid() == 0
	r.NetAdmin = r.Root || hasCapNetAdmin()
	r.IPCommand = h.runner().LookPath("ip")
	r.VCanModule = moduleLoaded("vcan")

	if !r.IPCommand {
		r.Fail("未找到 ip 命令（iproute2），无法创建 vcan 总线")
	}
	if !r.NetAdmin {
		r.Fail("缺少 CAP_NET_ADMIN：请用 sudo 运行，或执行 setcap cap_net_admin+ep <可执行文件>")
	}
	if !r.VCanModule {
		r.Warn("vcan 模块未加载，创建 CAN 总线时会尝试 modprobe vcan")
	}

	pairs := tty0ttyPairs()
	r.FreeTTYPairs = pairs
	switch {
	case pairs > 0:
		r.SerialBackend = model.SerialBackendTTY0TTY
	case fileExists("/dev/ptmx"):
		r.SerialBackend = model.SerialBackendPTY
		r.Warn("未检测到 tty0tty（/dev/tnt*），串口回退为 pty：设备仅本进程可见，外部 minicom 无法打开")
	default:
		r.Fail("既无 tty0tty 也无 /dev/ptmx，串口功能不可用")
	}
}

// ModuleLoaded 判断内核模块是否已加载。
func (h *Host) ModuleLoaded(name string) bool { return moduleLoaded(name) }

func hasCapNetAdmin() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		hex := strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
		v, err := strconv.ParseUint(hex, 16, 64)
		if err != nil {
			return false
		}
		return v&(1<<capNetAdmin) != 0
	}
	return false
}

func moduleLoaded(name string) bool {
	if fileExists("/sys/module/" + name) {
		return true
	}
	data, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, name+" ") {
			return true
		}
	}
	return false
}

// tty0ttyPairs 返回 /dev/tnt* 构成的成对设备数量。
func tty0ttyPairs() int {
	matches, err := filepath.Glob("/dev/tnt*")
	if err != nil {
		return 0
	}
	return len(matches) / 2
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
