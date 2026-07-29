// Package ui 是 Fyne 界面层：左侧拓扑画布，右侧属性 / 发送 / 日志。
// 本包只依赖 fyne 的纯 Go 部分（widget/container/canvas），app 包由 main 引入，
// 这样界面逻辑可以在没有 C 编译器的开发机上编译与测试。
package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lansonsam/buslab/internal/model"
	"github.com/lansonsam/buslab/internal/orch"
	"github.com/lansonsam/buslab/internal/persist"
)

const eventBatchSize = 128

type Options struct {
	Settings persist.Settings
	// CleanOrphans 清理上次异常退出遗留的内核资源，返回被清理的资源名。
	CleanOrphans func(context.Context) ([]string, error)
}

type UI struct {
	orch *orch.Orchestrator
	win  fyne.Window
	opts Options

	topo   *topology
	props  *propsPanel
	send   *sendPanel
	logs   *logPanel
	status *widget.Label

	project *model.Project
	sel     selection
	content fyne.CanvasObject

	events <-chan orch.Event
	cancel func()
	stop   chan struct{}
	wg     sync.WaitGroup
}

func New(o *orch.Orchestrator, win fyne.Window, opts Options) *UI {
	if opts.Settings.LogLimit <= 0 {
		opts.Settings = persist.DefaultSettings()
	}
	u := &UI{
		orch:   o,
		win:    win,
		opts:   opts,
		topo:   newTopology(),
		props:  newPropsPanel(),
		send:   newSendPanel(),
		logs:   newLogPanel(opts.Settings.LogLimit),
		status: widget.NewLabel(""),
		stop:   make(chan struct{}),
	}
	u.wire()
	u.content = u.buildLayout()
	u.reload()
	return u
}

func (u *UI) Content() fyne.CanvasObject { return u.content }

func (u *UI) buildLayout() fyne.CanvasObject {
	right := container.NewVSplit(
		u.props.content(),
		container.NewVSplit(u.send.content(), u.logs.content()),
	)
	right.Offset = 0.34

	main := container.NewHSplit(container.NewScroll(u.topo), right)
	main.Offset = 0.6

	return container.NewBorder(u.buildToolbar(), u.status, nil, nil, main)
}

func (u *UI) buildToolbar() fyne.CanvasObject {
	bar := widget.NewToolbar(
		widget.NewToolbarAction(theme.DocumentCreateIcon(), u.newProject),
		widget.NewToolbarAction(theme.FolderOpenIcon(), u.openProject),
		widget.NewToolbarAction(theme.DocumentSaveIcon(), u.saveProject),
		widget.NewToolbarSeparator(),
		widget.NewToolbarAction(theme.ContentAddIcon(), func() { u.addNode() }),
		widget.NewToolbarSeparator(),
		widget.NewToolbarAction(theme.MediaPlayIcon(), u.startAll),
		widget.NewToolbarAction(theme.MediaStopIcon(), u.stopAll),
		widget.NewToolbarSpacer(),
	)

	buttons := container.NewHBox()
	for _, t := range model.AllBusTypes {
		busType := t
		btn := widget.NewButton("+ "+busType.Label(), func() { u.createBus(busType) })
		if !u.orch.Supports(busType) {
			btn.Disable()
		}
		buttons.Add(btn)
	}
	return container.NewVBox(bar, buttons, widget.NewSeparator())
}

func (u *UI) wire() {
	u.topo.onSelect = func(sel selection) {
		u.sel = sel
		u.refreshPanels()
	}
	u.topo.onAttach = func(node model.NodeID, bus model.BusID) { u.attach(node, bus) }
	u.topo.onNodeMoved = u.orch.SetNodePos
	u.topo.onBusMoved = u.orch.SetBusPos

	u.props.onRename = func(sel selection, name string) {
		if sel.bus != "" {
			u.do(u.orch.RenameBus(sel.bus, name))
			return
		}
		u.do(u.orch.RenameNode(sel.node, name))
	}
	u.props.onSerial = func(bus model.BusID, params model.SerialParams) {
		u.do(u.orch.SetSerialParams(bus, params))
	}
	u.props.onCAN = func(bus model.BusID, params model.CANParams) {
		u.do(u.orch.SetCANParams(bus, params))
	}
	u.props.onStart = func(bus model.BusID) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		u.do(u.orch.StartBus(ctx, bus))
	}
	u.props.onStop = func(bus model.BusID) { u.do(u.orch.StopBus(bus)) }
	u.props.onDeleteBus = func(bus model.BusID) {
		if u.sel.bus == bus {
			u.sel = selection{}
		}
		u.do(u.orch.DeleteBus(bus))
	}
	u.props.onDeleteNode = func(node model.NodeID) {
		if u.sel.node == node {
			u.sel = selection{}
		}
		u.do(u.orch.DeleteNode(node))
	}
	u.props.onRole = func(node model.NodeID, role model.NodeRole) {
		u.do(u.orch.SetNodeRole(node, role))
	}
	u.props.onAttach = func(node model.NodeID, bus model.BusID) { u.attach(node, bus) }
	u.props.onDetach = func(node model.NodeID) { u.do(u.orch.DetachNode(node)) }
	if u.opts.CleanOrphans != nil {
		u.props.onCleanOrphan = u.cleanOrphans
	}

	u.send.onSend = func(node model.NodeID, frame model.Frame) {
		u.do(u.orch.SendFrame(node, frame))
	}
	u.send.onError = u.showError
}

// Start 启动事件泵；所有界面更新经 fyne.Do 回到主线程。
func (u *UI) Start() {
	events, cancel := u.orch.Subscribe(4096)
	u.events = events
	u.cancel = cancel

	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-u.stop:
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				batch := append(make([]orch.Event, 0, eventBatchSize), ev)
				batch = drain(events, batch)
				fyne.Do(func() { u.HandleEvents(batch) })
			case <-ticker.C:
				fyne.Do(func() {
					if u.topo.hasRecentActivity() {
						u.topo.Refresh()
					}
				})
			}
		}
	}()
}

func (u *UI) Stop() {
	close(u.stop)
	if u.cancel != nil {
		u.cancel()
	}
	u.wg.Wait()
}

func drain(events <-chan orch.Event, batch []orch.Event) []orch.Event {
	for len(batch) < eventBatchSize {
		select {
		case ev, ok := <-events:
			if !ok {
				return batch
			}
			batch = append(batch, ev)
		default:
			return batch
		}
	}
	return batch
}

// HandleEvents 处理一批事件，必须在主线程调用。
func (u *UI) HandleEvents(batch []orch.Event) {
	structural := false
	frames := false
	for _, ev := range batch {
		switch ev.Kind {
		case orch.EventFrame:
			frames = true
			u.appendFrame(ev.Frame)
		case orch.EventError:
			if ev.Err != nil {
				u.showError(fmt.Errorf("%s：%w", ev.Message, ev.Err))
			}
		default:
			structural = true
		}
	}
	if structural {
		u.reload()
		return
	}
	if frames {
		u.topo.Refresh()
		u.updateStatus()
	}
}

func (u *UI) appendFrame(f model.Frame) {
	busName, nodeName := string(f.Bus), string(f.Node)
	if b := u.project.Bus(f.Bus); b != nil {
		busName = b.Name
	}
	if n := u.project.Node(f.Node); n != nil {
		nodeName = n.Name
	}
	u.logs.append(f, busName, nodeName)
	u.topo.markActivity(f.Node)
}

func (u *UI) reload() {
	u.project = u.orch.Snapshot()
	u.pruneSelection()
	u.topo.SetProject(u.project)
	u.topo.SetSelection(u.sel)
	u.logs.setBuses(u.project.Buses)
	u.refreshPanels()
	u.updateStatus()
}

func (u *UI) refreshPanels() {
	u.props.update(u.project, u.sel, u.orch.HostReport())
	u.send.update(u.project, u.sel)
}

func (u *UI) pruneSelection() {
	if u.sel.bus != "" && u.project.Bus(u.sel.bus) == nil {
		u.sel = selection{}
	}
	if u.sel.node != "" && u.project.Node(u.sel.node) == nil {
		u.sel = selection{}
	}
}

func (u *UI) updateStatus() {
	running := 0
	for _, b := range u.project.Buses {
		if b.Running {
			running++
		}
	}
	report := u.orch.HostReport()
	text := fmt.Sprintf("%s · 总线 %d（运行 %d）· 节点 %d · 日志 %d 条",
		report.StatusLine(), len(u.project.Buses), running, len(u.project.Nodes), u.logs.rowCount())
	if dropped := u.orch.DroppedEvents(); dropped > 0 {
		text += fmt.Sprintf(" · 丢弃事件 %d", dropped)
	}
	if len(report.Errors) > 0 {
		text += " · " + report.Errors[0]
	}
	u.status.SetText(text)
}

func (u *UI) createBus(t model.BusType) {
	id, err := u.orch.CreateBus(t)
	if err != nil {
		u.showError(err)
		return
	}
	u.sel = selection{bus: id}
	u.reload()
}

func (u *UI) addNode() {
	id, err := u.orch.AddNode()
	if err != nil {
		u.showError(err)
		return
	}
	u.sel = selection{node: id}
	u.reload()
}

func (u *UI) attach(node model.NodeID, bus model.BusID) {
	role := model.RoleNode
	if b := u.project.Bus(bus); b != nil && b.Type == model.BusRS422 {
		role = model.RoleSlave
		if !hasMaster(u.project, bus) {
			role = model.RoleMaster
		}
	}
	u.do(u.orch.AttachNode(node, bus, role))
}

func hasMaster(p *model.Project, bus model.BusID) bool {
	for _, n := range p.NodesOf(bus) {
		if n.Role == model.RoleMaster {
			return true
		}
	}
	return false
}

func (u *UI) startAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var failures []error
	for _, b := range u.project.Buses {
		if b.Running {
			continue
		}
		if err := u.orch.StartBus(ctx, b.ID); err != nil {
			failures = append(failures, err)
		}
	}
	u.reload()
	if len(failures) > 0 {
		u.showError(failures[0])
	}
}

func (u *UI) stopAll() {
	err := u.orch.StopAll()
	u.reload()
	u.do(err)
}

func (u *UI) newProject() {
	dialog.ShowConfirm("新建实验", "将停止全部总线并清空当前拓扑，继续？", func(ok bool) {
		if !ok {
			return
		}
		u.sel = selection{}
		u.logs.clear()
		u.do(u.orch.Reset())
	}, u.win)
}

func (u *UI) openProject() {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			u.showError(err)
			return
		}
		if reader == nil {
			return
		}
		path := reader.URI().Path()
		_ = reader.Close()
		u.sel = selection{}
		u.do(u.orch.Load(path))
	}, u.win)
}

func (u *UI) saveProject() {
	if path := u.orch.ProjectPath(); path != "" {
		u.do(u.orch.Save(path))
		return
	}
	dialog.ShowFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			u.showError(err)
			return
		}
		if writer == nil {
			return
		}
		path := writer.URI().Path()
		_ = writer.Close()
		u.do(u.orch.Save(path))
	}, u.win)
}

func (u *UI) cleanOrphans() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cleaned, err := u.opts.CleanOrphans(ctx)
	if err != nil {
		u.showError(err)
		return
	}
	msg := "没有发现残留资源"
	if len(cleaned) > 0 {
		msg = fmt.Sprintf("已清理 %d 个残留接口：%v", len(cleaned), cleaned)
	}
	if u.win != nil {
		dialog.ShowInformation("清理完成", msg, u.win)
	}
}

// do 统一处理命令返回值：出错弹窗，成功则刷新界面。
func (u *UI) do(err error) {
	if err != nil {
		u.showError(err)
	}
	u.reload()
}

func (u *UI) showError(err error) {
	if err == nil {
		return
	}
	if u.win == nil {
		return
	}
	dialog.ShowError(err, u.win)
}
