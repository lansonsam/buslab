package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/lansonsam/buslab/internal/model"
)

const (
	nodeWidth      = 108
	nodeHeight     = 46
	busLength      = 520
	snapDistance   = 34
	activityWindow = 500 * time.Millisecond
)

// selection 表示画布上当前选中的对象，两个字段互斥。
type selection struct {
	bus  model.BusID
	node model.NodeID
}

func (s selection) empty() bool { return s.bus == "" && s.node == "" }

// topology 是左侧拓扑画布：自绘总线母线与节点方块，并处理拖拽/点选。
// 画布内的图元是普通 canvas 对象，不接收事件，全部命中判定在本控件完成。
type topology struct {
	widget.BaseWidget

	project  *model.Project
	sel      selection
	activity map[model.NodeID]time.Time

	onSelect    func(selection)
	onAttach    func(model.NodeID, model.BusID)
	onNodeMoved func(model.NodeID, model.Point)
	onBusMoved  func(model.BusID, model.Point)

	dragTarget selection
}

func newTopology() *topology {
	t := &topology{
		project:  model.NewProject(""),
		activity: map[model.NodeID]time.Time{},
	}
	t.ExtendBaseWidget(t)
	return t
}

func (t *topology) SetProject(p *model.Project) {
	if p == nil {
		p = model.NewProject("")
	}
	t.project = p
	t.Refresh()
}

func (t *topology) SetSelection(sel selection) {
	t.sel = sel
	t.Refresh()
}

func (t *topology) markActivity(node model.NodeID) {
	if node == "" {
		return
	}
	t.activity[node] = time.Now()
}

func (t *topology) hasRecentActivity() bool {
	for _, ts := range t.activity {
		if time.Since(ts) < activityWindow {
			return true
		}
	}
	return false
}

func (t *topology) Tapped(ev *fyne.PointEvent) {
	sel := t.hitTest(ev.Position)
	t.sel = sel
	t.Refresh()
	if t.onSelect != nil {
		t.onSelect(sel)
	}
}

func (t *topology) Dragged(ev *fyne.DragEvent) {
	if t.dragTarget.empty() {
		t.dragTarget = t.hitTest(ev.Position)
		if t.dragTarget.empty() {
			return
		}
		t.sel = t.dragTarget
		if t.onSelect != nil {
			t.onSelect(t.sel)
		}
	}
	switch {
	case t.dragTarget.node != "":
		if n := t.project.Node(t.dragTarget.node); n != nil {
			n.Pos.X = maxFloat(0, n.Pos.X+ev.Dragged.DX)
			n.Pos.Y = maxFloat(0, n.Pos.Y+ev.Dragged.DY)
		}
	case t.dragTarget.bus != "":
		if b := t.project.Bus(t.dragTarget.bus); b != nil {
			b.Pos.X = maxFloat(0, b.Pos.X+ev.Dragged.DX)
			b.Pos.Y = maxFloat(0, b.Pos.Y+ev.Dragged.DY)
		}
	}
	t.Refresh()
}

func (t *topology) DragEnd() {
	target := t.dragTarget
	t.dragTarget = selection{}
	switch {
	case target.node != "":
		n := t.project.Node(target.node)
		if n == nil {
			return
		}
		if t.onNodeMoved != nil {
			t.onNodeMoved(n.ID, n.Pos)
		}
		if bus := t.snapTarget(n); bus != "" && bus != n.Bus && t.onAttach != nil {
			t.onAttach(n.ID, bus)
		}
	case target.bus != "":
		if b := t.project.Bus(target.bus); b != nil && t.onBusMoved != nil {
			t.onBusMoved(b.ID, b.Pos)
		}
	}
}

// snapTarget 返回节点当前位置吸附到的总线（若有）。
func (t *topology) snapTarget(n *model.Node) model.BusID {
	cx, cy := n.Pos.X+nodeWidth/2, n.Pos.Y+nodeHeight/2
	best := model.BusID("")
	bestDist := float32(snapDistance)
	for _, b := range t.project.Buses {
		if cx < b.Pos.X-snapDistance || cx > b.Pos.X+busLength+snapDistance {
			continue
		}
		d := absFloat(cy - b.Pos.Y)
		if d <= bestDist {
			bestDist = d
			best = b.ID
		}
	}
	return best
}

func (t *topology) hitTest(p fyne.Position) selection {
	for i := len(t.project.Nodes) - 1; i >= 0; i-- {
		n := t.project.Nodes[i]
		if p.X >= n.Pos.X && p.X <= n.Pos.X+nodeWidth && p.Y >= n.Pos.Y && p.Y <= n.Pos.Y+nodeHeight {
			return selection{node: n.ID}
		}
	}
	for i := len(t.project.Buses) - 1; i >= 0; i-- {
		b := t.project.Buses[i]
		if p.X < b.Pos.X-8 || p.X > b.Pos.X+busLength+8 {
			continue
		}
		// 命中范围包含母线本身与其上方的标签行。
		if p.Y >= b.Pos.Y-24 && p.Y <= b.Pos.Y+10 {
			return selection{bus: b.ID}
		}
	}
	return selection{}
}

func (t *topology) CreateRenderer() fyne.WidgetRenderer {
	r := &topologyRenderer{topo: t, bg: canvas.NewRectangle(canvasBgColor)}
	r.rebuild()
	return r
}

type topologyRenderer struct {
	topo    *topology
	bg      *canvas.Rectangle
	objects []fyne.CanvasObject
	size    fyne.Size
}

func (r *topologyRenderer) rebuild() {
	t := r.topo
	objs := []fyne.CanvasObject{r.bg}

	for _, b := range t.project.Buses {
		c := busColor(b.Type)
		line := canvas.NewLine(c)
		line.StrokeWidth = 5
		if !b.Running {
			line.StrokeColor = fade(c, 0x88)
			line.StrokeWidth = 3
		}
		line.Position1 = fyne.NewPos(b.Pos.X, b.Pos.Y)
		line.Position2 = fyne.NewPos(b.Pos.X+busLength, b.Pos.Y)
		objs = append(objs, line)

		if t.sel.bus == b.ID {
			marker := canvas.NewRectangle(fade(c, 0x33))
			marker.StrokeColor = selectStroke
			marker.StrokeWidth = 1
			marker.Move(fyne.NewPos(b.Pos.X-6, b.Pos.Y-26))
			marker.Resize(fyne.NewSize(busLength+12, 34))
			objs = append(objs, marker)
		}

		label := canvas.NewText(busLabel(b), c)
		label.TextSize = 13
		label.TextStyle = fyne.TextStyle{Bold: true}
		label.Move(fyne.NewPos(b.Pos.X, b.Pos.Y-22))
		objs = append(objs, label)

		if b.Resource != "" {
			res := canvas.NewText(b.Resource, mutedColor)
			res.TextSize = 11
			res.Move(fyne.NewPos(b.Pos.X+busLength-140, b.Pos.Y-20))
			objs = append(objs, res)
		}
	}

	for _, n := range t.project.Nodes {
		if bus := t.project.Bus(n.Bus); bus != nil {
			link := canvas.NewLine(fade(busColor(bus.Type), 0xAA))
			link.StrokeWidth = 2
			cx := clampFloat(n.Pos.X+nodeWidth/2, bus.Pos.X, bus.Pos.X+busLength)
			link.Position1 = fyne.NewPos(cx, bus.Pos.Y)
			link.Position2 = fyne.NewPos(n.Pos.X+nodeWidth/2, n.Pos.Y+nodeHeight/2)
			objs = append(objs, link)
		}
	}

	for _, n := range t.project.Nodes {
		box := canvas.NewRectangle(nodeFillIdle)
		box.CornerRadius = 4
		box.StrokeWidth = 1
		box.StrokeColor = nodeStroke
		if bus := t.project.Bus(n.Bus); bus != nil {
			box.FillColor = nodeFill
			box.StrokeColor = busColor(bus.Type)
		}
		if ts, ok := t.activity[n.ID]; ok && time.Since(ts) < activityWindow {
			box.StrokeWidth = 3
		}
		if t.sel.node == n.ID {
			box.StrokeColor = selectStroke
			box.StrokeWidth = 2
		}
		box.Move(fyne.NewPos(n.Pos.X, n.Pos.Y))
		box.Resize(fyne.NewSize(nodeWidth, nodeHeight))
		objs = append(objs, box)

		name := canvas.NewText(n.Name, textColor)
		name.TextSize = 12
		name.Move(fyne.NewPos(n.Pos.X+8, n.Pos.Y+6))
		objs = append(objs, name)

		sub := canvas.NewText(nodeSubtitle(n), mutedColor)
		sub.TextSize = 10
		sub.Move(fyne.NewPos(n.Pos.X+8, n.Pos.Y+25))
		objs = append(objs, sub)
	}

	if len(t.project.Buses) == 0 && len(t.project.Nodes) == 0 {
		hint := canvas.NewText("用左上角工具栏新建总线与节点，再把节点拖到母线上完成连线", mutedColor)
		hint.TextSize = 13
		hint.Move(fyne.NewPos(32, 32))
		objs = append(objs, hint)
	}

	r.objects = objs
	r.applyBackground()
}

func (r *topologyRenderer) applyBackground() {
	size := r.size
	if size.IsZero() {
		size = r.MinSize()
	}
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))
}

func (r *topologyRenderer) Layout(size fyne.Size) {
	r.size = size
	r.applyBackground()
}

func (r *topologyRenderer) MinSize() fyne.Size {
	width, height := float32(720), float32(420)
	for _, b := range r.topo.project.Buses {
		width = maxFloat(width, b.Pos.X+busLength+40)
		height = maxFloat(height, b.Pos.Y+80)
	}
	for _, n := range r.topo.project.Nodes {
		width = maxFloat(width, n.Pos.X+nodeWidth+40)
		height = maxFloat(height, n.Pos.Y+nodeHeight+40)
	}
	return fyne.NewSize(width, height)
}

func (r *topologyRenderer) Refresh() {
	r.rebuild()
	canvas.Refresh(r.topo)
}

func (r *topologyRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *topologyRenderer) Destroy() {}

func busLabel(b *model.Bus) string {
	status := "已停止"
	if b.Running {
		status = "运行中"
	}
	return fmt.Sprintf("%s · %s · %s", b.Name, b.Type.Label(), status)
}

func nodeSubtitle(n *model.Node) string {
	if n.Endpoint != "" {
		return n.Endpoint
	}
	if n.Attached() {
		return n.Role.Label() + " · 未启动"
	}
	return "未连接"
}

func maxFloat(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func absFloat(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func clampFloat(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
