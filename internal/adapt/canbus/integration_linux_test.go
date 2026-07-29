//go:build linux && integration

// 真实内核集成测试：会创建并删除 vcanbl* 接口，需要 root 或 CAP_NET_ADMIN。
// 运行：go test -tags integration ./internal/adapt/canbus/
package canbus

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/lansonsam/buslab/internal/host"
	"github.com/lansonsam/buslab/internal/model"
)

func TestVCANRoundTrip(t *testing.T) {
	h := host.New()
	report := h.Detect(context.Background())
	if !report.CANAvailable() {
		t.Skipf("跳过：CAN 不可用（%v %v）", report.Errors, report.Warnings)
	}

	var mu sync.Mutex
	var frames []model.Frame
	sink := func(f model.Frame) {
		mu.Lock()
		frames = append(frames, f)
		mu.Unlock()
	}

	spec := model.BusSpec{ID: "bus1", Name: "集成测试", Type: model.BusCAN, CAN: model.DefaultCANParams()}
	bus, err := NewProvider(h).Create(context.Background(), spec, sink)
	if err != nil {
		t.Fatalf("创建 vcan 失败：%v", err)
	}
	ifname := bus.Resource()
	if _, err := net.InterfaceByName(ifname); err != nil {
		t.Fatalf("接口 %s 不可见：%v", ifname, err)
	}

	epA, err := bus.Open("n1", model.RoleNode)
	if err != nil {
		t.Fatalf("打开接入点 A 失败：%v", err)
	}
	epB, err := bus.Open("n2", model.RoleNode)
	if err != nil {
		t.Fatalf("打开接入点 B 失败：%v", err)
	}
	_ = epB

	if err := epA.Send(model.Frame{Kind: model.BusCAN, CANID: 0x321, Data: []byte{0xAA, 0xBB}}); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var rx *model.Frame
	for time.Now().Before(deadline) {
		mu.Lock()
		for i := range frames {
			if frames[i].Dir == model.DirRx && frames[i].Node == "n2" {
				f := frames[i]
				rx = &f
				break
			}
		}
		mu.Unlock()
		if rx != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rx == nil {
		t.Fatal("对端未收到帧")
	}
	if rx.CANID != 0x321 || len(rx.Data) != 2 || rx.Data[0] != 0xAA {
		t.Fatalf("收到的帧不符：%+v", rx)
	}

	if err := bus.Close(); err != nil {
		t.Fatalf("关闭总线失败：%v", err)
	}
	if _, err := net.InterfaceByName(ifname); err == nil {
		t.Fatalf("接口 %s 未被删除", ifname)
	}
}
