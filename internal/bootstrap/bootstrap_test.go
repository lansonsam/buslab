package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lansonsam/buslab/internal/host"
	"github.com/lansonsam/buslab/internal/model"
)

func TestRegistryFallsBackToMemory(t *testing.T) {
	report := model.HostReport{Supported: false}
	reg := Registry(host.New(), &report)
	for _, t2 := range model.AllBusTypes {
		if !reg.Supports(t2) {
			t.Fatalf("模拟后端应支持 %s", t2)
		}
	}
	if len(report.Warnings) == 0 {
		t.Fatal("回退到模拟后端应追加警告")
	}
}

func TestRegistryUsesKernelBackends(t *testing.T) {
	report := model.HostReport{Supported: true, SerialBackend: model.SerialBackendTTY0TTY}
	reg := Registry(host.New(), &report)
	if !reg.Supports(model.BusCAN) || !reg.Supports(model.BusRS485) {
		t.Fatal("内核后端应覆盖 CAN 与串口总线")
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("受支持平台不应追加警告：%v", report.Warnings)
	}
}

func TestOrphanCleanerDeletesOnlyOwnInterfaces(t *testing.T) {
	fake := &host.FakeRunner{Links: []string{"lo", "eth0", "vcanbl1", "vcanbl7", "can0"}}
	cleaned, err := OrphanCleaner(&host.Host{Runner: fake})(context.Background())
	if err != nil {
		t.Fatalf("清理失败：%v", err)
	}
	if len(cleaned) != 2 || cleaned[0] != "vcanbl1" || cleaned[1] != "vcanbl7" {
		t.Fatalf("清理结果不符：%v", cleaned)
	}
	for _, call := range fake.Calls() {
		if strings.HasPrefix(call, "ip link delete") && !strings.Contains(call, "vcanbl") {
			t.Fatalf("不应删除非本程序接口：%s", call)
		}
	}
}

func TestOrphanCleanerReportsError(t *testing.T) {
	fake := &host.FakeRunner{
		Links: []string{"vcanbl1"},
		Handler: func(name string, args []string) (host.Result, error, bool) {
			if len(args) > 2 && args[1] == "delete" {
				return host.Result{Stderr: "Operation not permitted"}, errors.New("exit 2"), true
			}
			return host.Result{}, nil, false
		},
	}
	cleaned, err := OrphanCleaner(&host.Host{Runner: fake})(context.Background())
	if host.KindOf(err) != host.KindPermission {
		t.Fatalf("期望权限错误，得到 %v", err)
	}
	if len(cleaned) != 0 {
		t.Fatalf("失败时不应报告已清理：%v", cleaned)
	}
}
