package ui

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lansonsam/buslab/internal/model"
)

const detachOption = "未连接"

// propsPanel 是右上角属性面板，内容随选中对象重建。
type propsPanel struct {
	box  *fyne.Container
	root fyne.CanvasObject

	onRename      func(selection, string)
	onSerial      func(model.BusID, model.SerialParams)
	onCAN         func(model.BusID, model.CANParams)
	onStart       func(model.BusID)
	onStop        func(model.BusID)
	onDeleteBus   func(model.BusID)
	onDeleteNode  func(model.NodeID)
	onRole        func(model.NodeID, model.NodeRole)
	onAttach      func(model.NodeID, model.BusID)
	onDetach      func(model.NodeID)
	onCleanOrphan func()
}

func layoutForm() fyne.Layout { return layout.NewFormLayout() }

func newPropsPanel() *propsPanel {
	p := &propsPanel{box: container.NewVBox()}
	p.root = container.NewVScroll(p.box)
	return p
}

func (p *propsPanel) content() fyne.CanvasObject { return p.root }

func (p *propsPanel) update(project *model.Project, sel selection, report model.HostReport) {
	p.box.RemoveAll()
	switch {
	case sel.bus != "":
		if bus := project.Bus(sel.bus); bus != nil {
			p.buildBus(project, bus)
		}
	case sel.node != "":
		if node := project.Node(sel.node); node != nil {
			p.buildNode(project, node)
		}
	default:
		p.buildOverview(project, report)
	}
	p.box.Refresh()
}

func (p *propsPanel) buildOverview(project *model.Project, report model.HostReport) {
	p.box.Add(widget.NewLabelWithStyle("宿主环境", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	p.box.Add(container.New(layoutForm(),
		widget.NewLabel("系统"), widget.NewLabel(report.OS),
		widget.NewLabel("CAN"), widget.NewLabel(boolLabel(report.CANAvailable(), "可用", "不可用")),
		widget.NewLabel("串口后端"), widget.NewLabel(report.SerialBackend.Label()),
		widget.NewLabel("vcan 模块"), widget.NewLabel(boolLabel(report.VCanModule, "已加载", "未加载")),
	))
	for _, msg := range report.Errors {
		p.box.Add(warningLabel("✕ " + msg))
	}
	for _, msg := range report.Warnings {
		p.box.Add(warningLabel("! " + msg))
	}
	if p.onCleanOrphan != nil {
		p.box.Add(widget.NewButtonWithIcon("清理残留 vcan 接口", theme.DeleteIcon(), p.onCleanOrphan))
	}

	p.box.Add(widget.NewSeparator())
	p.box.Add(widget.NewLabelWithStyle("拓扑提示", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	issues := model.BusIssues(project)
	if len(issues) == 0 {
		p.box.Add(widget.NewLabel("在画布上选择总线或节点以编辑属性"))
		return
	}
	for _, issue := range issues {
		p.box.Add(warningLabel("· " + issue.Message))
	}
}

func (p *propsPanel) buildBus(project *model.Project, bus *model.Bus) {
	name := widget.NewEntry()
	name.SetText(bus.Name)
	name.OnSubmitted = func(text string) {
		if p.onRename != nil {
			p.onRename(selection{bus: bus.ID}, text)
		}
	}
	resource := bus.Resource
	if resource == "" {
		resource = "未分配"
	}
	nodes := project.NodesOf(bus.ID)

	p.box.Add(widget.NewLabelWithStyle(bus.Type.Label()+" 总线", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	p.box.Add(container.New(layoutForm(),
		widget.NewLabel("名称"), name,
		widget.NewLabel("状态"), widget.NewLabel(boolLabel(bus.Running, "运行中", "已停止")),
		widget.NewLabel("内核资源"), widget.NewLabel(resource),
		widget.NewLabel("节点数"), widget.NewLabel(strconv.Itoa(len(nodes))),
	))

	if bus.Type.IsSerial() {
		p.box.Add(widget.NewSeparator())
		p.box.Add(p.serialForm(bus))
	} else {
		p.box.Add(widget.NewSeparator())
		p.box.Add(p.canForm(bus))
	}

	if err := model.CheckBusReady(project, bus.ID); err != nil {
		p.box.Add(warningLabel("! " + err.Error()))
	}

	start := widget.NewButtonWithIcon("启动", theme.MediaPlayIcon(), func() {
		if p.onStart != nil {
			p.onStart(bus.ID)
		}
	})
	stop := widget.NewButtonWithIcon("停止", theme.MediaStopIcon(), func() {
		if p.onStop != nil {
			p.onStop(bus.ID)
		}
	})
	if bus.Running {
		start.Disable()
	} else {
		stop.Disable()
	}
	del := widget.NewButtonWithIcon("删除总线", theme.DeleteIcon(), func() {
		if p.onDeleteBus != nil {
			p.onDeleteBus(bus.ID)
		}
	})
	del.Importance = widget.DangerImportance
	p.box.Add(container.NewHBox(start, stop, del))

	if len(nodes) > 0 {
		p.box.Add(widget.NewSeparator())
		p.box.Add(widget.NewLabelWithStyle("已连接节点", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		for _, n := range nodes {
			endpoint := n.Endpoint
			if endpoint == "" {
				endpoint = "未分配"
			}
			p.box.Add(widget.NewLabel(fmt.Sprintf("· %s（%s）→ %s", n.Name, n.Role.Label(), endpoint)))
		}
	}
}

func (p *propsPanel) serialForm(bus *model.Bus) fyne.CanvasObject {
	params := bus.Serial
	apply := func() {
		if p.onSerial != nil {
			p.onSerial(bus.ID, params)
		}
	}

	baudOptions := make([]string, 0, len(model.StandardBaudRates))
	for _, b := range model.StandardBaudRates {
		baudOptions = append(baudOptions, strconv.Itoa(b))
	}
	baud := widget.NewSelect(baudOptions, func(v string) {
		n, err := strconv.Atoi(v)
		if err != nil || n == params.BaudRate {
			return
		}
		params.BaudRate = n
		apply()
	})
	baud.SetSelected(strconv.Itoa(params.BaudRate))

	dataBits := widget.NewSelect([]string{"5", "6", "7", "8"}, func(v string) {
		n, err := strconv.Atoi(v)
		if err != nil || n == params.DataBits {
			return
		}
		params.DataBits = n
		apply()
	})
	dataBits.SetSelected(strconv.Itoa(params.DataBits))

	parityLabels := map[model.Parity]string{model.ParityNone: "无", model.ParityEven: "偶", model.ParityOdd: "奇"}
	parity := widget.NewSelect([]string{"无", "偶", "奇"}, func(v string) {
		for k, label := range parityLabels {
			if label == v && k != params.Parity {
				params.Parity = k
				apply()
				return
			}
		}
	})
	parity.SetSelected(parityLabels[params.Parity])

	stopBits := widget.NewSelect([]string{"1", "2"}, func(v string) {
		n, err := strconv.Atoi(v)
		if err != nil || n == params.StopBits {
			return
		}
		params.StopBits = n
		apply()
	})
	stopBits.SetSelected(strconv.Itoa(params.StopBits))

	if bus.Running {
		for _, w := range []*widget.Select{baud, dataBits, parity, stopBits} {
			w.Disable()
		}
	}

	form := container.New(layoutForm(),
		widget.NewLabel("波特率"), baud,
		widget.NewLabel("数据位"), dataBits,
		widget.NewLabel("校验"), parity,
		widget.NewLabel("停止位"), stopBits,
	)
	notes := []fyne.CanvasObject{form}
	switch bus.Type {
	case model.BusRS485:
		notes = append(notes, hintLabel("485 多点广播由应用层 Hub 完成（内核无虚拟 485 总线）"))
	case model.BusRS422:
		notes = append(notes, hintLabel("422：主机发送给所有从机，从机只回主机"))
	case model.BusRS232:
		notes = append(notes, hintLabel("232 为点对点，最多两个节点"))
	}
	if bus.Running {
		notes = append(notes, hintLabel("运行中不可修改线路参数，请先停止"))
	}
	return container.NewVBox(notes...)
}

func (p *propsPanel) canForm(bus *model.Bus) fyne.CanvasObject {
	bitrate := widget.NewEntry()
	bitrate.SetText(strconv.Itoa(bus.CAN.Bitrate))
	bitrate.OnSubmitted = func(text string) {
		n, err := strconv.Atoi(text)
		if err != nil || n <= 0 {
			bitrate.SetText(strconv.Itoa(bus.CAN.Bitrate))
			return
		}
		if p.onCAN != nil {
			p.onCAN(bus.ID, model.CANParams{Bitrate: n, FD: bus.CAN.FD})
		}
	}
	return container.NewVBox(
		container.New(layoutForm(), widget.NewLabel("标称比特率"), bitrate),
		hintLabel("vcan 不做位定时，比特率仅作记录"),
	)
}

func (p *propsPanel) buildNode(project *model.Project, node *model.Node) {
	name := widget.NewEntry()
	name.SetText(node.Name)
	name.OnSubmitted = func(text string) {
		if p.onRename != nil {
			p.onRename(selection{node: node.ID}, text)
		}
	}

	roleLabels := map[model.NodeRole]string{
		model.RoleNode: "节点", model.RoleMaster: "主机", model.RoleSlave: "从机",
	}
	role := widget.NewSelect([]string{"节点", "主机", "从机"}, func(v string) {
		for k, label := range roleLabels {
			if label == v && k != node.Role && p.onRole != nil {
				p.onRole(node.ID, k)
				return
			}
		}
	})
	role.SetSelected(roleLabels[node.Role])

	busOptions := []string{detachOption}
	busByLabel := map[string]model.BusID{detachOption: ""}
	selected := detachOption
	for _, b := range project.Buses {
		label := fmt.Sprintf("%s（%s）", b.Name, b.Type.Label())
		busOptions = append(busOptions, label)
		busByLabel[label] = b.ID
		if b.ID == node.Bus {
			selected = label
		}
	}
	busSelect := widget.NewSelect(busOptions, func(v string) {
		target := busByLabel[v]
		if target == node.Bus {
			return
		}
		if target == "" {
			if p.onDetach != nil {
				p.onDetach(node.ID)
			}
			return
		}
		if p.onAttach != nil {
			p.onAttach(node.ID, target)
		}
	})
	busSelect.SetSelected(selected)

	endpoint := node.Endpoint
	if endpoint == "" {
		endpoint = "未分配"
	}

	p.box.Add(widget.NewLabelWithStyle("节点", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	p.box.Add(container.New(layoutForm(),
		widget.NewLabel("名称"), name,
		widget.NewLabel("角色"), role,
		widget.NewLabel("所属总线"), busSelect,
		widget.NewLabel("接入点"), widget.NewLabel(endpoint),
	))
	if node.Attached() {
		if bus := project.Bus(node.Bus); bus != nil && bus.Type == model.BusRS422 {
			p.box.Add(hintLabel("RS-422 总线上必须恰好有一个主机"))
		}
	}

	del := widget.NewButtonWithIcon("删除节点", theme.DeleteIcon(), func() {
		if p.onDeleteNode != nil {
			p.onDeleteNode(node.ID)
		}
	})
	del.Importance = widget.DangerImportance
	p.box.Add(container.NewHBox(del))
}

func boolLabel(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}

func hintLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = widget.LowImportance
	return l
}

func warningLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = widget.WarningImportance
	return l
}
