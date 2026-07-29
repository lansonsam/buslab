//go:build linux && integration

// 真实虚拟串口集成测试：优先使用 tty0tty，其次回退 pty。
// 运行：go test -tags integration ./internal/adapt/serialbus/
package serialbus

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/host"
	"github.com/lansonsam/buslab/internal/model"
)

func TestRealTTYBroadcast(t *testing.T) {
	report := host.New().Detect(context.Background())
	if !report.SerialAvailable() {
		t.Skipf("跳过：串口后端不可用（%v）", report.Errors)
	}

	var mu sync.Mutex
	var frames []model.Frame
	sink := func(f model.Frame) {
		mu.Lock()
		frames = append(frames, f)
		mu.Unlock()
	}

	spec := model.BusSpec{ID: "bus1", Name: "集成测试", Type: model.BusRS485, Serial: model.DefaultSerialParams()}
	bus, err := NewProvider(report.SerialBackend).Create(context.Background(), spec, sink)
	if err != nil {
		t.Fatalf("创建串口总线失败：%v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	var eps []adapt.Endpoint
	for _, node := range []model.NodeID{"n1", "n2"} {
		ep, err := bus.Open(node, model.RoleNode)
		if err != nil {
			t.Fatalf("为 %s 分配端口失败：%v", node, err)
		}
		eps = append(eps, ep)
	}
	for _, ep := range eps {
		if _, err := os.Stat(ep.Name()); err != nil {
			t.Fatalf("外部设备 %s 不可访问：%v", ep.Name(), err)
		}
	}

	payload := []byte("PING")
	if err := eps[0].Send(model.Frame{Kind: model.BusRS485, Data: payload}); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(frames)
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) < 2 {
		t.Fatalf("应至少产生发送与接收两个事件，实际 %d 个", len(frames))
	}
	if frames[0].Dir != model.DirTx || frames[0].Node != "n1" {
		t.Fatalf("首个事件应为 n1 的发送：%+v", frames[0])
	}
	if frames[1].Node != "n2" || string(frames[1].Data) != string(payload) {
		t.Fatalf("n2 未正确收到数据：%+v", frames[1])
	}
}

// TestExternalToolCanOpenPort 验证外部进程可打开另一端（tty0tty 场景的关键能力）。
func TestExternalToolCanOpenPort(t *testing.T) {
	report := host.New().Detect(context.Background())
	if report.SerialBackend != model.SerialBackendTTY0TTY {
		t.Skip("跳过：仅在 tty0tty 后端下验证外部可见性")
	}
	spec := model.BusSpec{ID: "bus1", Name: "外部互通", Type: model.BusRS232, Serial: model.DefaultSerialParams()}
	bus, err := NewProvider(report.SerialBackend).Create(context.Background(), spec, func(model.Frame) {})
	if err != nil {
		t.Fatalf("创建总线失败：%v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	ep, err := bus.Open("n1", model.RoleNode)
	if err != nil {
		t.Fatalf("分配端口失败：%v", err)
	}
	f, err := os.OpenFile(ep.Name(), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("外部无法打开 %s：%v", ep.Name(), err)
	}
	_ = f.Close()
}
