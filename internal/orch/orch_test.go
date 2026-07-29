package orch

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/adapt/fake"
	"github.com/lansonsam/buslab/internal/model"
	"github.com/lansonsam/buslab/internal/persist"
)

func newTestOrch(t *testing.T) (*Orchestrator, *eventLog) {
	t.Helper()
	o := New(fake.Registry(), model.HostReport{Supported: true, SerialBackend: model.SerialBackendPTY})
	log := newEventLog(o)
	t.Cleanup(func() { _ = o.Close() })
	return o, log
}

type eventLog struct {
	mu     sync.Mutex
	events []Event
	stop   func()
}

func newEventLog(o *Orchestrator) *eventLog {
	ch, cancel := o.Subscribe(256)
	l := &eventLog{stop: cancel}
	go func() {
		for ev := range ch {
			l.mu.Lock()
			l.events = append(l.events, ev)
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *eventLog) frames() []model.Frame {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []model.Frame
	for _, ev := range l.events {
		if ev.Kind == EventFrame {
			out = append(out, ev.Frame)
		}
	}
	return out
}

func (l *eventLog) waitFrames(t *testing.T, n int) []model.Frame {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := l.frames(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 %d 个帧事件超时，实际 %d 个", n, len(l.frames()))
	return nil
}

func (l *eventLog) has(kind EventKind) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for _, ev := range l.events {
			if ev.Kind == kind {
				l.mu.Unlock()
				return true
			}
		}
		l.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func attachNodes(t *testing.T, o *Orchestrator, bus model.BusID, roles ...model.NodeRole) []model.NodeID {
	t.Helper()
	var ids []model.NodeID
	for _, role := range roles {
		id, err := o.AddNode()
		if err != nil {
			t.Fatalf("添加节点失败：%v", err)
		}
		if err := o.AttachNode(id, bus, role); err != nil {
			t.Fatalf("连接节点失败：%v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestCANSendFanout(t *testing.T) {
	o, log := newTestOrch(t)
	bus, err := o.CreateBus(model.BusCAN)
	if err != nil {
		t.Fatalf("创建总线失败：%v", err)
	}
	nodes := attachNodes(t, o, bus, model.RoleNode, model.RoleNode)
	if err := o.StartBus(context.Background(), bus); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	if err := o.SendFrame(nodes[0], model.Frame{CANID: 0x123, Data: []byte{1, 2, 3}}); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	frames := log.waitFrames(t, 2)
	if frames[0].Dir != model.DirTx || frames[0].Node != nodes[0] || frames[0].Seq != 1 {
		t.Fatalf("首帧应为发送方 TX 且序号为 1：%+v", frames[0])
	}
	if frames[1].Dir != model.DirRx || frames[1].Node != nodes[1] || frames[1].Kind != model.BusCAN {
		t.Fatalf("次帧应为对端 RX：%+v", frames[1])
	}
	if frames[1].Seq != 2 {
		t.Fatalf("序号应递增：%d", frames[1].Seq)
	}

	snap := o.Snapshot()
	if !snap.Bus(bus).Running || snap.Bus(bus).Resource == "" {
		t.Fatalf("运行状态未反映到快照：%+v", snap.Bus(bus))
	}
	if snap.Node(nodes[0]).Endpoint == "" {
		t.Fatal("接入点名称未记录")
	}
}

func TestSendRequiresRunningBus(t *testing.T) {
	o, _ := newTestOrch(t)
	bus, _ := o.CreateBus(model.BusCAN)
	nodes := attachNodes(t, o, bus, model.RoleNode)
	if err := o.SendFrame(nodes[0], model.Frame{CANID: 1, Data: []byte{1}}); err == nil {
		t.Fatal("未启动总线时发送应报错")
	}
	free, _ := o.AddNode()
	if err := o.SendFrame(free, model.Frame{Data: []byte{1}}); err == nil {
		t.Fatal("未连接节点发送应报错")
	}
	if err := o.SendFrame("ghost", model.Frame{Data: []byte{1}}); err == nil {
		t.Fatal("未知节点发送应报错")
	}
}

func TestAttachValidationAndDetach(t *testing.T) {
	o, _ := newTestOrch(t)
	bus, _ := o.CreateBus(model.BusRS232)
	nodes := attachNodes(t, o, bus, model.RoleNode, model.RoleNode)
	third, _ := o.AddNode()
	if err := o.AttachNode(third, bus, model.RoleNode); err == nil {
		t.Fatal("RS-232 第三个节点应被拒绝")
	}
	if err := o.AttachNode(nodes[0], bus, model.RoleNode); err == nil {
		t.Fatal("重复连接应被拒绝")
	}
	if err := o.DetachNode(nodes[0]); err != nil {
		t.Fatalf("断开失败：%v", err)
	}
	if o.Snapshot().Node(nodes[0]).Attached() {
		t.Fatal("断开后仍显示已连接")
	}
	if err := o.AttachNode(third, bus, model.RoleNode); err != nil {
		t.Fatalf("腾出位置后应可连接：%v", err)
	}
}

func TestAttachWhileRunningOpensEndpoint(t *testing.T) {
	o, _ := newTestOrch(t)
	bus, _ := o.CreateBus(model.BusRS485)
	attachNodes(t, o, bus, model.RoleNode)
	if err := o.StartBus(context.Background(), bus); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	late, _ := o.AddNode()
	if err := o.AttachNode(late, bus, model.RoleNode); err != nil {
		t.Fatalf("运行中连接失败：%v", err)
	}
	if o.Snapshot().Node(late).Endpoint == "" {
		t.Fatal("运行中连接应立即分配接入点")
	}
	if err := o.SendFrame(late, model.Frame{Data: []byte("x")}); err != nil {
		t.Fatalf("运行中连接后应可发送：%v", err)
	}
}

func TestStartBusRejectsIncompleteTopology(t *testing.T) {
	o, _ := newTestOrch(t)
	bus, _ := o.CreateBus(model.BusRS422)
	if err := o.StartBus(context.Background(), bus); err == nil {
		t.Fatal("空总线不应可启动")
	}
	attachNodes(t, o, bus, model.RoleSlave)
	if err := o.StartBus(context.Background(), bus); err == nil {
		t.Fatal("RS-422 缺少主机不应可启动")
	}
	attachNodes(t, o, bus, model.RoleMaster)
	if err := o.StartBus(context.Background(), bus); err != nil {
		t.Fatalf("1 主 1 从应可启动：%v", err)
	}
}

type failingProvider struct {
	inner    adapt.Provider
	failFrom int

	mu     sync.Mutex
	closed int
}

func (p *failingProvider) Supports(t model.BusType) bool { return p.inner.Supports(t) }

func (p *failingProvider) Create(ctx context.Context, spec model.BusSpec, sink adapt.Sink) (adapt.Bus, error) {
	bus, err := p.inner.Create(ctx, spec, sink)
	if err != nil {
		return nil, err
	}
	return &failingBus{Bus: bus, provider: p}, nil
}

type failingBus struct {
	adapt.Bus
	provider *failingProvider
	opened   int
}

func (b *failingBus) Open(node model.NodeID, role model.NodeRole) (adapt.Endpoint, error) {
	b.opened++
	if b.opened >= b.provider.failFrom {
		return nil, errors.New("模拟接入点创建失败")
	}
	return b.Bus.Open(node, role)
}

func (b *failingBus) Close() error {
	b.provider.mu.Lock()
	b.provider.closed++
	b.provider.mu.Unlock()
	return b.Bus.Close()
}

func TestStartBusRollsBackOnEndpointFailure(t *testing.T) {
	provider := &failingProvider{inner: &fake.CANProvider{}, failFrom: 2}
	o := New(adapt.NewRegistry(provider), model.HostReport{Supported: true})
	t.Cleanup(func() { _ = o.Close() })

	bus, _ := o.CreateBus(model.BusCAN)
	attachNodes(t, o, bus, model.RoleNode, model.RoleNode)
	if err := o.StartBus(context.Background(), bus); err == nil {
		t.Fatal("第二个接入点失败时启动应报错")
	}
	snap := o.Snapshot()
	if snap.Bus(bus).Running || snap.Bus(bus).Resource != "" {
		t.Fatalf("失败后不应留下半启动状态：%+v", snap.Bus(bus))
	}
	provider.mu.Lock()
	closed := provider.closed
	provider.mu.Unlock()
	if closed != 1 {
		t.Fatalf("失败后应释放总线资源，Close 次数 = %d", closed)
	}
	for _, n := range snap.Nodes {
		if n.Endpoint != "" {
			t.Fatalf("节点 %s 不应留下接入点", n.Name)
		}
	}
}

func TestStopBusAndStopAll(t *testing.T) {
	o, log := newTestOrch(t)
	canBus, _ := o.CreateBus(model.BusCAN)
	serialBus, _ := o.CreateBus(model.BusRS485)
	attachNodes(t, o, canBus, model.RoleNode, model.RoleNode)
	attachNodes(t, o, serialBus, model.RoleNode, model.RoleNode)
	for _, b := range []model.BusID{canBus, serialBus} {
		if err := o.StartBus(context.Background(), b); err != nil {
			t.Fatalf("启动 %s 失败：%v", b, err)
		}
	}
	if err := o.StopBus(canBus); err != nil {
		t.Fatalf("停止失败：%v", err)
	}
	snap := o.Snapshot()
	if snap.Bus(canBus).Running || snap.Bus(canBus).Resource != "" {
		t.Fatal("停止后状态未清理")
	}
	if !snap.Bus(serialBus).Running {
		t.Fatal("不应影响其它总线")
	}
	if err := o.StopBus(canBus); err != nil {
		t.Fatalf("重复停止应幂等：%v", err)
	}
	if err := o.StopAll(); err != nil {
		t.Fatalf("StopAll 失败：%v", err)
	}
	if o.Snapshot().Bus(serialBus).Running {
		t.Fatal("StopAll 后仍在运行")
	}
	if !log.has(EventBusChanged) {
		t.Fatal("缺少 BusChanged 事件")
	}
}

func TestDeleteBusStopsAndDetaches(t *testing.T) {
	o, log := newTestOrch(t)
	bus, _ := o.CreateBus(model.BusCAN)
	nodes := attachNodes(t, o, bus, model.RoleNode, model.RoleNode)
	if err := o.StartBus(context.Background(), bus); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	if err := o.DeleteBus(bus); err != nil {
		t.Fatalf("删除总线失败：%v", err)
	}
	snap := o.Snapshot()
	if snap.Bus(bus) != nil {
		t.Fatal("总线未删除")
	}
	for _, id := range nodes {
		if n := snap.Node(id); n.Attached() || n.Endpoint != "" {
			t.Fatalf("节点 %s 未被断开", n.Name)
		}
	}
	if !log.has(EventBusDeleted) {
		t.Fatal("缺少 BusDeleted 事件")
	}
	if err := o.DeleteNode(nodes[0]); err != nil {
		t.Fatalf("删除节点失败：%v", err)
	}
	if o.Snapshot().Node(nodes[0]) != nil {
		t.Fatal("节点未删除")
	}
	if err := o.DeleteBus("ghost"); err == nil {
		t.Fatal("删除未知总线应报错")
	}
}

func TestSetNodeRoleRewiresRunningBus(t *testing.T) {
	o, _ := newTestOrch(t)
	bus, _ := o.CreateBus(model.BusRS422)
	nodes := attachNodes(t, o, bus, model.RoleMaster, model.RoleSlave)
	if err := o.StartBus(context.Background(), bus); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	if err := o.SetNodeRole(nodes[1], model.RoleMaster); err == nil {
		t.Fatal("第二个主机应被拒绝")
	}
	if o.Snapshot().Node(nodes[1]).Role != model.RoleSlave {
		t.Fatal("失败的角色变更不应生效")
	}
	if err := o.SetNodeRole(nodes[1], model.RoleNode); err != nil {
		t.Fatalf("角色变更失败：%v", err)
	}
	if snap := o.Snapshot().Node(nodes[1]); snap.Role != model.RoleNode || snap.Endpoint == "" {
		t.Fatalf("角色变更后应重建接入点：%+v", snap)
	}
}

func TestParamEditingGuards(t *testing.T) {
	o, _ := newTestOrch(t)
	bus, _ := o.CreateBus(model.BusRS485)
	attachNodes(t, o, bus, model.RoleNode, model.RoleNode)
	params := model.SerialParams{BaudRate: 9600, DataBits: 8, Parity: model.ParityEven, StopBits: 1}
	if err := o.SetSerialParams(bus, params); err != nil {
		t.Fatalf("设置串口参数失败：%v", err)
	}
	if err := o.StartBus(context.Background(), bus); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	if err := o.SetSerialParams(bus, model.DefaultSerialParams()); err == nil {
		t.Fatal("运行中修改串口参数应被拒绝")
	}
	if err := o.SetCANParams(bus, model.DefaultCANParams()); err == nil {
		t.Fatal("对串口总线设置 CAN 参数应报错")
	}
	if err := o.RenameBus(bus, "  "); err == nil {
		t.Fatal("空名称应被拒绝")
	}
	if err := o.RenameBus(bus, "我的 485"); err != nil {
		t.Fatalf("重命名失败：%v", err)
	}
	if o.Snapshot().Bus(bus).Name != "我的 485" {
		t.Fatal("重命名未生效")
	}
}

func TestSaveLoadAndReset(t *testing.T) {
	o, log := newTestOrch(t)
	bus, _ := o.CreateBus(model.BusCAN)
	nodes := attachNodes(t, o, bus, model.RoleNode, model.RoleNode)
	if err := o.StartBus(context.Background(), bus); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	o.SetNodePos(nodes[0], model.Point{X: 11, Y: 22})

	path := filepath.Join(t.TempDir(), "proj")
	if err := o.Save(path); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	want := persist.EnsureExtension(path)
	if o.ProjectPath() != want {
		t.Fatalf("项目路径 = %q，期望 %q", o.ProjectPath(), want)
	}

	if err := o.Reset(); err != nil {
		t.Fatalf("重置失败：%v", err)
	}
	if len(o.Snapshot().Buses) != 0 {
		t.Fatal("重置后应为空项目")
	}
	if err := o.Load(want); err != nil {
		t.Fatalf("加载失败：%v", err)
	}
	snap := o.Snapshot()
	if len(snap.Buses) != 1 || len(snap.Nodes) != 2 {
		t.Fatalf("加载结果不符：%d 总线 %d 节点", len(snap.Buses), len(snap.Nodes))
	}
	if snap.Buses[0].Running {
		t.Fatal("加载后总线应为停止状态")
	}
	if snap.Node(nodes[0]).Pos.X != 11 {
		t.Fatal("坐标未持久化")
	}
	if !log.has(EventProjectReplaced) {
		t.Fatal("缺少 ProjectReplaced 事件")
	}
	if err := o.Load(filepath.Join(t.TempDir(), "missing.buslab.json")); err == nil {
		t.Fatal("加载缺失文件应报错")
	}
}

func TestUnsupportedBusType(t *testing.T) {
	o := New(adapt.NewRegistry(&fake.CANProvider{}), model.HostReport{Supported: true})
	t.Cleanup(func() { _ = o.Close() })
	if _, err := o.CreateBus("modbus"); err == nil {
		t.Fatal("未知类型应报错")
	}
	bus, _ := o.CreateBus(model.BusRS485)
	attachNodes(t, o, bus, model.RoleNode, model.RoleNode)
	if err := o.StartBus(context.Background(), bus); err == nil {
		t.Fatal("无对应 Provider 时启动应报错")
	}
	if o.Supports(model.BusRS485) {
		t.Fatal("Supports 应返回 false")
	}
}

func TestHostReportEvent(t *testing.T) {
	o, log := newTestOrch(t)
	o.SetHostReport(model.HostReport{Supported: true, Root: true, IPCommand: true,
		SerialBackend: model.SerialBackendTTY0TTY})
	if !log.has(EventHostStatus) {
		t.Fatal("缺少 HostStatus 事件")
	}
	if !o.HostReport().CANAvailable() {
		t.Fatal("root + ip 应判定 CAN 可用")
	}
}
