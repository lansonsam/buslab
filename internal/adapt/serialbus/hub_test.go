package serialbus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/model"
)

type fakePort struct {
	external string
	in       chan []byte
	out      chan []byte
	closed   chan struct{}
	once     sync.Once
}

func newFakePort(name string) *fakePort {
	return &fakePort{
		external: name,
		in:       make(chan []byte, 16),
		out:      make(chan []byte, 16),
		closed:   make(chan struct{}),
	}
}

func (p *fakePort) Read(b []byte) (int, error) {
	select {
	case data := <-p.in:
		return copy(b, data), nil
	case <-p.closed:
		return 0, fmt.Errorf("port closed")
	}
}

func (p *fakePort) Write(b []byte) (int, error) {
	select {
	case <-p.closed:
		return 0, fmt.Errorf("port closed")
	case p.out <- append([]byte(nil), b...):
		return len(b), nil
	default:
		return 0, fmt.Errorf("缓冲区已满")
	}
}

func (p *fakePort) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func (p *fakePort) External() string { return p.external }

func (p *fakePort) recv(t *testing.T) []byte {
	t.Helper()
	select {
	case data := <-p.out:
		return data
	case <-time.After(time.Second):
		t.Fatalf("%s 未收到数据", p.external)
		return nil
	}
}

func (p *fakePort) expectSilent(t *testing.T) {
	t.Helper()
	select {
	case data := <-p.out:
		t.Fatalf("%s 不应收到数据，实际 %X", p.external, data)
	case <-time.After(80 * time.Millisecond):
	}
}

type fakeFactory struct {
	mu    sync.Mutex
	n     int
	ports map[model.NodeID]*fakePort
	err   error
}

func newFakeFactory() *fakeFactory {
	return &fakeFactory{ports: map[model.NodeID]*fakePort{}}
}

func (f *fakeFactory) Backend() model.SerialBackend { return model.SerialBackendPTY }

func (f *fakeFactory) Open(_ model.BusSpec, node model.NodeID) (Port, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.n++
	p := newFakePort(fmt.Sprintf("/dev/fake%d", f.n))
	f.ports[node] = p
	return p, nil
}

func (f *fakeFactory) port(node model.NodeID) *fakePort {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ports[node]
}

type collector struct {
	mu     sync.Mutex
	frames []model.Frame
}

func (c *collector) sink(f model.Frame) {
	c.mu.Lock()
	c.frames = append(c.frames, f)
	c.mu.Unlock()
}

func (c *collector) wait(t *testing.T, n int) []model.Frame {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := len(c.frames)
		c.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]model.Frame(nil), c.frames...)
}

func setupBus(t *testing.T, kind model.BusType, nodes map[model.NodeID]model.NodeRole) (
	adapt.Bus, *fakeFactory, *collector, map[model.NodeID]adapt.Endpoint) {
	t.Helper()
	factory := newFakeFactory()
	col := &collector{}
	spec := model.BusSpec{ID: "bus1", Name: "测试总线", Type: kind, Serial: model.DefaultSerialParams()}
	bus, err := NewProviderWithFactory(factory).Create(context.Background(), spec, col.sink)
	if err != nil {
		t.Fatalf("创建总线失败：%v", err)
	}
	eps := map[model.NodeID]adapt.Endpoint{}
	ids := []model.NodeID{"n1", "n2", "n3"}
	for _, id := range ids {
		role, ok := nodes[id]
		if !ok {
			continue
		}
		ep, err := bus.Open(id, role)
		if err != nil {
			t.Fatalf("接入 %s 失败：%v", id, err)
		}
		eps[id] = ep
	}
	t.Cleanup(func() { _ = bus.Close() })
	return bus, factory, col, eps
}

func TestRS232PointToPoint(t *testing.T) {
	_, factory, col, eps := setupBus(t, model.BusRS232, map[model.NodeID]model.NodeRole{
		"n1": model.RoleNode, "n2": model.RoleNode,
	})
	if err := eps["n1"].Send(model.Frame{Kind: model.BusRS232, Data: []byte("AB")}); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if got := string(factory.port("n2").recv(t)); got != "AB" {
		t.Fatalf("对端收到 %q", got)
	}
	frames := col.wait(t, 2)
	if len(frames) != 2 || frames[0].Dir != model.DirTx || frames[0].Node != "n1" {
		t.Fatalf("事件序列异常：%+v", frames)
	}
	if frames[1].Dir != model.DirRx || frames[1].Node != "n2" {
		t.Fatalf("接收事件异常：%+v", frames[1])
	}
}

func TestRS232RejectsThirdEndpoint(t *testing.T) {
	bus, _, _, _ := setupBus(t, model.BusRS232, map[model.NodeID]model.NodeRole{
		"n1": model.RoleNode, "n2": model.RoleNode,
	})
	if _, err := bus.Open("n3", model.RoleNode); err == nil {
		t.Fatal("RS-232 第三个接入点应被拒绝")
	}
}

func TestRS485Broadcast(t *testing.T) {
	_, factory, _, eps := setupBus(t, model.BusRS485, map[model.NodeID]model.NodeRole{
		"n1": model.RoleNode, "n2": model.RoleNode, "n3": model.RoleNode,
	})
	if err := eps["n2"].Send(model.Frame{Kind: model.BusRS485, Data: []byte{0x01, 0x02}}); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	for _, id := range []model.NodeID{"n1", "n3"} {
		if got := factory.port(id).recv(t); len(got) != 2 || got[0] != 1 {
			t.Fatalf("%s 收到 %X", id, got)
		}
	}
	factory.port("n2").expectSilent(t)
}

func TestRS422MasterSlaveDirections(t *testing.T) {
	_, factory, _, eps := setupBus(t, model.BusRS422, map[model.NodeID]model.NodeRole{
		"n1": model.RoleMaster, "n2": model.RoleSlave, "n3": model.RoleSlave,
	})
	if err := eps["n1"].Send(model.Frame{Kind: model.BusRS422, Data: []byte("M")}); err != nil {
		t.Fatalf("主机发送失败：%v", err)
	}
	factory.port("n2").recv(t)
	factory.port("n3").recv(t)

	if err := eps["n2"].Send(model.Frame{Kind: model.BusRS422, Data: []byte("S")}); err != nil {
		t.Fatalf("从机发送失败：%v", err)
	}
	if got := string(factory.port("n1").recv(t)); got != "S" {
		t.Fatalf("主机收到 %q", got)
	}
	factory.port("n3").expectSilent(t)
}

func TestExternalWriteIsForwarded(t *testing.T) {
	_, factory, col, _ := setupBus(t, model.BusRS485, map[model.NodeID]model.NodeRole{
		"n1": model.RoleNode, "n2": model.RoleNode,
	})
	factory.port("n1").in <- []byte("EXT")
	if got := string(factory.port("n2").recv(t)); got != "EXT" {
		t.Fatalf("转发结果 %q", got)
	}
	frames := col.wait(t, 2)
	if frames[0].Node != "n1" || frames[0].Dir != model.DirTx {
		t.Fatalf("外部写入应记为 n1 的发送：%+v", frames[0])
	}
}

func TestCollisionNoteOnlyForRS485(t *testing.T) {
	now := time.Now()
	col := &collector{}
	hub := NewHub(model.BusSpec{ID: "b", Type: model.BusRS485}, col.sink)
	hub.now = func() time.Time { return now }
	for _, id := range []model.NodeID{"n1", "n2"} {
		if err := hub.Add(id, model.RoleNode, newFakePort(string(id))); err != nil {
			t.Fatalf("Add 失败：%v", err)
		}
	}
	t.Cleanup(func() { _ = hub.Close() })

	if err := hub.Send("n1", []byte("a")); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if err := hub.Send("n2", []byte("b")); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	frames := col.wait(t, 4)
	if frames[2].Note == "" {
		t.Fatalf("同一时刻不同节点发送应标注冲突：%+v", frames[2])
	}

	now = now.Add(time.Second)
	if err := hub.Send("n1", []byte("c")); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	frames = col.wait(t, 6)
	if frames[4].Note != "" {
		t.Fatalf("间隔足够时不应标注冲突：%+v", frames[4])
	}
}

func TestEndpointCloseAndBusClose(t *testing.T) {
	bus, factory, _, eps := setupBus(t, model.BusRS485, map[model.NodeID]model.NodeRole{
		"n1": model.RoleNode, "n2": model.RoleNode,
	})
	if err := eps["n1"].Close(); err != nil {
		t.Fatalf("关闭接入点失败：%v", err)
	}
	if err := eps["n1"].Send(model.Frame{Kind: model.BusRS485, Data: []byte("x")}); err == nil {
		t.Fatal("已关闭的接入点不应能发送")
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("关闭总线失败：%v", err)
	}
	select {
	case <-factory.port("n2").closed:
	default:
		t.Fatal("总线关闭后端口应被释放")
	}
	if _, err := bus.Open("n3", model.RoleNode); err == nil {
		t.Fatal("已关闭总线不应能接入新节点")
	}
}

func TestUnsupportedWithoutFactory(t *testing.T) {
	p := NewProviderWithFactory(nil)
	if _, err := p.Create(context.Background(), model.BusSpec{Type: model.BusRS232}, nil); err == nil {
		t.Fatal("无工厂应返回不支持")
	}
	if p.Backend() != model.SerialBackendNone {
		t.Fatalf("Backend = %v", p.Backend())
	}
	if !p.Supports(model.BusRS485) || p.Supports(model.BusCAN) {
		t.Fatal("Supports 判定错误")
	}
}
