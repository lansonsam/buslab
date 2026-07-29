// Package bootstrap 组装后端依赖，使 main 只保留窗口相关代码
// （main 需要 fyne 的 app 包，无法在没有 C 编译器的开发机上编译）。
package bootstrap

import (
	"context"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/adapt/canbus"
	"github.com/lansonsam/buslab/internal/adapt/fake"
	"github.com/lansonsam/buslab/internal/adapt/serialbus"
	"github.com/lansonsam/buslab/internal/host"
	"github.com/lansonsam/buslab/internal/model"
)

// Registry 依据宿主能力选择真实内核后端或内存模拟后端。
// 平台不支持时会在 report 上追加警告，供状态栏与属性面板展示。
func Registry(h *host.Host, report *model.HostReport) *adapt.Registry {
	if report.Supported {
		return adapt.NewRegistry(canbus.NewProvider(h), serialbus.NewProvider(report.SerialBackend))
	}
	report.Warn("当前平台使用内存模拟后端：不会创建任何内核资源，仅用于界面预览")
	return fake.Registry()
}

// OrphanCleaner 返回清理上次异常退出遗留 vcan 接口的函数。
func OrphanCleaner(h *host.Host) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		orphans, err := h.OrphanVCANs(ctx)
		if err != nil {
			return nil, err
		}
		cleaned := make([]string, 0, len(orphans))
		for _, name := range orphans {
			if err := h.DeleteLink(ctx, name); err != nil {
				return cleaned, err
			}
			cleaned = append(cleaned, name)
		}
		return cleaned, nil
	}
}
