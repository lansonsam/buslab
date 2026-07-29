//go:build !linux

package host

import (
	"context"
	"runtime"

	"github.com/lansonsam/buslab/internal/model"
)

func platformName() string { return runtime.GOOS }

// ModuleLoaded 在非 Linux 平台恒为 false。
func (h *Host) ModuleLoaded(string) bool { return false }

func probePlatform(_ context.Context, _ *Host, r *model.HostReport) {
	r.Supported = false
	r.Fail("虚拟总线实验室的内核后端仅支持 Linux，当前系统为 %s：可浏览界面但无法创建总线", runtime.GOOS)
}
