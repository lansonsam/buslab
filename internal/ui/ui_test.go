package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/lansonsam/buslab/internal/adapt/fake"
	"github.com/lansonsam/buslab/internal/model"
	"github.com/lansonsam/buslab/internal/orch"
	"github.com/lansonsam/buslab/internal/persist"
)

type harness struct {
	ui     *UI
	orch   *orch.Orchestrator
	events <-chan orch.Event
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	test.NewTempApp(t)

	o := orch.New(fake.Registry(), model.HostReport{
		OS: "linux", Supported: true, Root: true, IPCommand: true, VCanModule: true,
		SerialBackend: model.SerialBackendTTY0TTY,
	})
	events, cancel := o.Subscribe(1024)
	u := New(o, nil, Options{Settings: persist.DefaultSettings()})
	win := test.NewWindow(u.Content())
	win.Resize(fyne.NewSize(1200, 800))
	t.Cleanup(func() {
		cancel()
		_ = o.Close()
		win.Close()
	})
	return &harness{ui: u, orch: o, events: events}
}

// pump 把已产生的事件交给界面处理，模拟事件泵在主线程上的行为。
func (h *harness) pump(t *testing.T, minFrames int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	frames := 0
	for {
		var batch []orch.Event
		for {
			select {
			case ev := <-h.events:
				batch = append(batch, ev)
				if ev.Kind == orch.EventFrame {
					frames++
				}
				continue
			default:
			}
			break
		}
		if len(batch) > 0 {
			h.ui.HandleEvents(batch)
		}
		if frames >= minFrames || time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) buildCANBus(t *testing.T) (model.BusID, []model.NodeID) {
	t.Helper()
	h.ui.createBus(model.BusCAN)
	bus := h.ui.sel.bus
	if bus == "" {
		t.Fatal("创建总线后应自动选中")
	}
	var nodes []model.NodeID
	for i := 0; i < 2; i++ {
		h.ui.addNode()
		node := h.ui.sel.node
		if node == "" {
			t.Fatal("添加节点后应自动选中")
		}
		h.ui.attach(node, bus)
		nodes = append(nodes, node)
	}
	h.ui.sel = selection{bus: bus}
	h.ui.reload()
	return bus, nodes
}

func TestCreateBusAttachAndSend(t *testing.T) {
	h := newHarness(t)
	bus, nodes := h.buildCANBus(t)

	if got := len(h.ui.project.NodesOf(bus)); got != 2 {
		t.Fatalf("总线上应有 2 个节点，实际 %d", got)
	}
	if h.ui.send.sendable {
		t.Fatal("总线未启动时不应允许发送")
	}

	h.ui.props.onStart(bus)
	if !h.ui.project.Bus(bus).Running {
		t.Fatal("启动后应处于运行状态")
	}
	if !h.ui.send.sendable {
		t.Fatal("启动后应允许发送")
	}

	h.ui.send.nodeSelect.SetSelected(h.ui.project.Node(nodes[0]).Name)
	h.ui.send.idEntry.SetText("1A2")
	h.ui.send.dataEntry.SetText("de ad be ef")
	h.ui.send.submit()

	h.pump(t, 2)
	if got := h.ui.logs.rowCount(); got != 2 {
		t.Fatalf("应记录发送与接收各一条，实际 %d 条", got)
	}
	row := h.ui.logs.rows[0]
	if row.columns[4] != "发送" || row.columns[5] != "ID=0x1A2 [4] DE AD BE EF" {
		t.Fatalf("日志内容不符：%v", row.columns)
	}
	if h.ui.logs.rows[1].columns[4] != "接收" {
		t.Fatalf("第二条应为接收：%v", h.ui.logs.rows[1].columns)
	}
	if !h.ui.topo.hasRecentActivity() {
		t.Fatal("收发后画布应有活动高亮")
	}
}

func TestSerialSendUsesFormat(t *testing.T) {
	h := newHarness(t)
	h.ui.createBus(model.BusRS485)
	bus := h.ui.sel.bus
	for i := 0; i < 2; i++ {
		h.ui.addNode()
		h.ui.attach(h.ui.sel.node, bus)
	}
	h.ui.sel = selection{bus: bus}
	h.ui.reload()
	h.ui.props.onStart(bus)

	if h.ui.send.kind != model.BusRS485 {
		t.Fatalf("发送面板应切换为串口模式，实际 %v", h.ui.send.kind)
	}
	h.ui.send.formatSel.SetSelected("Hex")
	h.ui.send.textEntry.SetText("01 FF")
	h.ui.send.submit()
	h.pump(t, 2)

	if got := h.ui.logs.rows[0].columns[5]; got != "01 FF" {
		t.Fatalf("Hex 发送内容不符：%q", got)
	}

	h.ui.send.formatSel.SetSelected("ASCII")
	h.ui.send.textEntry.SetText("Hi")
	h.ui.send.submit()
	h.pump(t, 4)
	if got := h.ui.logs.rows[2].columns[5]; got != "48 69  |Hi|" {
		t.Fatalf("ASCII 发送内容不符：%q", got)
	}
}

func TestSendPanelRejectsBadInput(t *testing.T) {
	h := newHarness(t)
	bus, _ := h.buildCANBus(t)
	h.ui.props.onStart(bus)

	var captured error
	h.ui.send.onError = func(err error) { captured = err }
	h.ui.send.dataEntry.SetText("zz")
	h.ui.send.submit()
	if captured == nil {
		t.Fatal("非法十六进制应报错")
	}

	captured = nil
	h.ui.send.dataEntry.SetText("01 02 03 04 05 06 07 08 09")
	h.ui.send.submit()
	if captured == nil {
		t.Fatal("超过 8 字节应报错")
	}
}

func TestTopologyDragSnapsToBus(t *testing.T) {
	h := newHarness(t)
	h.ui.createBus(model.BusCAN)
	bus := h.ui.sel.bus
	h.ui.addNode()
	node := h.ui.sel.node

	project := h.ui.project
	b := project.Bus(bus)
	n := project.Node(node)
	n.Pos = model.Point{X: b.Pos.X + 100, Y: b.Pos.Y + 200}
	h.ui.topo.SetProject(project)

	start := fyne.NewPos(n.Pos.X+10, n.Pos.Y+10)
	h.ui.topo.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: start},
		Dragged:    fyne.NewDelta(0, -(200 + nodeHeight/2)),
	})
	if h.ui.topo.dragTarget.node != node {
		t.Fatalf("拖拽目标应为节点，实际 %+v", h.ui.topo.dragTarget)
	}
	h.ui.topo.DragEnd()

	if got := h.ui.orch.Snapshot().Node(node); got.Bus != bus {
		t.Fatalf("拖到母线附近应自动连接，实际 %q", got.Bus)
	}
	if h.ui.orch.Snapshot().Node(node).Pos.Y == 0 {
		t.Fatal("拖拽后位置应被提交")
	}
}

func TestTopologyTapSelectsBusAndNode(t *testing.T) {
	h := newHarness(t)
	h.ui.createBus(model.BusRS232)
	bus := h.ui.sel.bus
	h.ui.addNode()
	node := h.ui.sel.node
	h.ui.reload()

	b := h.ui.project.Bus(bus)
	h.ui.topo.Tapped(&fyne.PointEvent{Position: fyne.NewPos(b.Pos.X+40, b.Pos.Y)})
	if h.ui.sel.bus != bus {
		t.Fatalf("点击母线应选中总线，实际 %+v", h.ui.sel)
	}
	n := h.ui.project.Node(node)
	h.ui.topo.Tapped(&fyne.PointEvent{Position: fyne.NewPos(n.Pos.X+5, n.Pos.Y+5)})
	if h.ui.sel.node != node {
		t.Fatalf("点击节点应选中节点，实际 %+v", h.ui.sel)
	}
	h.ui.topo.Tapped(&fyne.PointEvent{Position: fyne.NewPos(2000, 2000)})
	if !h.ui.sel.empty() {
		t.Fatalf("点击空白应清空选择，实际 %+v", h.ui.sel)
	}
}

func TestRS232ThirdNodeShowsErrorAndKeepsTopology(t *testing.T) {
	h := newHarness(t)
	h.ui.createBus(model.BusRS232)
	bus := h.ui.sel.bus
	for i := 0; i < 3; i++ {
		h.ui.addNode()
		h.ui.attach(h.ui.sel.node, bus)
	}
	if got := len(h.ui.orch.Snapshot().NodesOf(bus)); got != 2 {
		t.Fatalf("RS-232 上应只有 2 个节点，实际 %d", got)
	}
}

func TestRS422AutoRoleAssignment(t *testing.T) {
	h := newHarness(t)
	h.ui.createBus(model.BusRS422)
	bus := h.ui.sel.bus
	var nodes []model.NodeID
	for i := 0; i < 3; i++ {
		h.ui.addNode()
		nodes = append(nodes, h.ui.sel.node)
		h.ui.attach(h.ui.sel.node, bus)
	}
	snap := h.ui.orch.Snapshot()
	if snap.Node(nodes[0]).Role != model.RoleMaster {
		t.Fatalf("首个节点应成为主机，实际 %v", snap.Node(nodes[0]).Role)
	}
	for _, id := range nodes[1:] {
		if snap.Node(id).Role != model.RoleSlave {
			t.Fatalf("后续节点应为从机，实际 %v", snap.Node(id).Role)
		}
	}
	if err := h.orch.StartBus(context.Background(), bus); err != nil {
		t.Fatalf("1 主 2 从应可启动：%v", err)
	}
}

func TestLogFilterAndPause(t *testing.T) {
	h := newHarness(t)
	canBus, canNodes := h.buildCANBus(t)
	h.ui.props.onStart(canBus)
	h.ui.send.nodeSelect.SetSelected(h.ui.project.Node(canNodes[0]).Name)
	h.ui.send.submit()
	h.pump(t, 2)

	h.ui.logs.setFilter("ghost")
	if len(h.ui.logs.view) != 0 {
		t.Fatal("过滤到不存在的总线应无可见行")
	}
	h.ui.logs.setFilter(canBus)
	if len(h.ui.logs.view) != 2 {
		t.Fatalf("按总线过滤后应有 2 行，实际 %d", len(h.ui.logs.view))
	}

	h.ui.logs.togglePause()
	h.ui.send.submit()
	h.pump(t, 4)
	if got := h.ui.logs.rowCount(); got != 2 {
		t.Fatalf("暂停期间不应记录，实际 %d 行", got)
	}
	h.ui.logs.togglePause()
	h.ui.send.submit()
	h.pump(t, 6)
	if got := h.ui.logs.rowCount(); got <= 2 {
		t.Fatalf("恢复后应继续记录，实际 %d 行", got)
	}

	h.ui.logs.clear()
	if h.ui.logs.rowCount() != 0 || len(h.ui.logs.view) != 0 {
		t.Fatal("清空后应无记录")
	}
}

func TestLogRingBufferLimit(t *testing.T) {
	h := newHarness(t)
	h.ui.logs.limit = 3
	for i := 0; i < 6; i++ {
		h.ui.logs.append(model.Frame{Seq: uint64(i), Kind: model.BusCAN, Time: time.Now(),
			Dir: model.DirTx, Data: []byte{byte(i)}}, "bus", "node")
	}
	if got := h.ui.logs.rowCount(); got != 3 {
		t.Fatalf("环形缓冲应保留 3 条，实际 %d", got)
	}
	if h.ui.logs.rows[0].columns[0] != "3" {
		t.Fatalf("应保留最新记录，首行序号 %q", h.ui.logs.rows[0].columns[0])
	}
}

func TestPropsPanelBuildsForSelections(t *testing.T) {
	h := newHarness(t)
	bus, nodes := h.buildCANBus(t)

	h.ui.sel = selection{bus: bus}
	h.ui.refreshPanels()
	if len(h.ui.props.box.Objects) == 0 {
		t.Fatal("总线属性面板为空")
	}

	h.ui.props.onRename(selection{bus: bus}, "我的 CAN")
	if h.ui.orch.Snapshot().Bus(bus).Name != "我的 CAN" {
		t.Fatal("总线重命名未生效")
	}

	h.ui.sel = selection{node: nodes[0]}
	h.ui.refreshPanels()
	h.ui.props.onRename(selection{node: nodes[0]}, "ECU-A")
	if h.ui.orch.Snapshot().Node(nodes[0]).Name != "ECU-A" {
		t.Fatal("节点重命名未生效")
	}
	h.ui.props.onDetach(nodes[0])
	if h.ui.orch.Snapshot().Node(nodes[0]).Attached() {
		t.Fatal("断开未生效")
	}

	h.ui.sel = selection{}
	h.ui.refreshPanels()
	if len(h.ui.props.box.Objects) == 0 {
		t.Fatal("概览面板为空")
	}
}

func TestDeleteBusAndNodeClearSelection(t *testing.T) {
	h := newHarness(t)
	bus, nodes := h.buildCANBus(t)
	h.ui.props.onStart(bus)

	h.ui.sel = selection{node: nodes[0]}
	h.ui.props.onDeleteNode(nodes[0])
	if h.ui.orch.Snapshot().Node(nodes[0]) != nil {
		t.Fatal("节点未删除")
	}
	if !h.ui.sel.empty() {
		t.Fatal("删除后应清空选择")
	}

	h.ui.sel = selection{bus: bus}
	h.ui.props.onDeleteBus(bus)
	if h.ui.orch.Snapshot().Bus(bus) != nil {
		t.Fatal("总线未删除")
	}
	if !h.ui.sel.empty() {
		t.Fatal("删除总线后应清空选择")
	}
}

func TestStartAllStopAll(t *testing.T) {
	h := newHarness(t)
	canBus, _ := h.buildCANBus(t)
	h.ui.createBus(model.BusRS485)
	serialBus := h.ui.sel.bus
	for i := 0; i < 2; i++ {
		h.ui.addNode()
		h.ui.attach(h.ui.sel.node, serialBus)
	}
	h.ui.reload()

	h.ui.startAll()
	snap := h.ui.orch.Snapshot()
	if !snap.Bus(canBus).Running || !snap.Bus(serialBus).Running {
		t.Fatal("全部启动未生效")
	}
	h.ui.stopAll()
	snap = h.ui.orch.Snapshot()
	if snap.Bus(canBus).Running || snap.Bus(serialBus).Running {
		t.Fatal("全部停止未生效")
	}
}

func TestStatusBarReflectsState(t *testing.T) {
	h := newHarness(t)
	bus, _ := h.buildCANBus(t)
	h.ui.props.onStart(bus)
	text := h.ui.status.Text
	if text == "" {
		t.Fatal("状态栏为空")
	}
	for _, want := range []string{"CAN 就绪", "运行 1", "节点 2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("状态栏缺少 %q：%s", want, text)
		}
	}
}

func TestCleanOrphansCallback(t *testing.T) {
	test.NewTempApp(t)
	o := orch.New(fake.Registry(), model.HostReport{Supported: true})
	t.Cleanup(func() { _ = o.Close() })

	called := false
	u := New(o, nil, Options{
		Settings: persist.DefaultSettings(),
		CleanOrphans: func(context.Context) ([]string, error) {
			called = true
			return []string{"vcanbl1"}, nil
		},
	})
	if u.props.onCleanOrphan == nil {
		t.Fatal("提供清理函数时应显示清理按钮")
	}
	u.props.onCleanOrphan()
	if !called {
		t.Fatal("清理回调未被调用")
	}
}
