package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lansonsam/buslab/internal/model"
)

// sendPanel 按选中总线类型切换 CAN / 串口两套发送表单。
type sendPanel struct {
	onSend  func(model.NodeID, model.Frame)
	onError func(error)

	nodeSelect *widget.Select
	idEntry    *widget.Entry
	extCheck   *widget.Check
	rtrCheck   *widget.Check
	dataEntry  *widget.Entry
	textEntry  *widget.Entry
	formatSel  *widget.RadioGroup
	sendButton *widget.Button
	hint       *widget.Label

	canForm    *fyne.Container
	serialForm *fyne.Container
	root       fyne.CanvasObject

	nodes    map[string]model.NodeID
	kind     model.BusType
	sendable bool
}

func newSendPanel() *sendPanel {
	s := &sendPanel{nodes: map[string]model.NodeID{}}

	s.nodeSelect = widget.NewSelect(nil, func(string) { s.updateState() })
	s.nodeSelect.PlaceHolder = "选择发送节点"

	s.idEntry = widget.NewEntry()
	s.idEntry.SetPlaceHolder("十六进制，如 123")
	s.idEntry.SetText("123")
	s.extCheck = widget.NewCheck("扩展帧", nil)
	s.rtrCheck = widget.NewCheck("远程帧", nil)
	s.dataEntry = widget.NewEntry()
	s.dataEntry.SetPlaceHolder("最多 8 字节，如 01 02 03")
	s.dataEntry.SetText("01 02 03 04")

	s.canForm = container.New(layoutForm(),
		widget.NewLabel("CAN ID"), s.idEntry,
		widget.NewLabel("数据"), s.dataEntry,
		widget.NewLabel("选项"), container.NewHBox(s.extCheck, s.rtrCheck),
	)

	s.textEntry = widget.NewMultiLineEntry()
	s.textEntry.SetPlaceHolder("按所选格式输入内容")
	s.textEntry.SetText("HELLO")
	s.formatSel = widget.NewRadioGroup([]string{"ASCII", "Hex"}, nil)
	s.formatSel.Horizontal = true
	s.formatSel.SetSelected("ASCII")

	s.serialForm = container.New(layoutForm(),
		widget.NewLabel("格式"), s.formatSel,
		widget.NewLabel("内容"), s.textEntry,
	)

	s.sendButton = widget.NewButtonWithIcon("发送", theme.MailSendIcon(), s.submit)
	s.sendButton.Importance = widget.HighImportance
	s.hint = widget.NewLabel("")
	s.hint.Wrapping = fyne.TextWrapWord

	s.root = container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("发送节点"), nil, s.nodeSelect),
		s.canForm,
		s.serialForm,
		container.NewHBox(s.sendButton),
		s.hint,
	)
	s.setKind(model.BusCAN)
	return s
}

func (s *sendPanel) content() fyne.CanvasObject { return s.root }

func (s *sendPanel) setKind(t model.BusType) {
	s.kind = t
	if t == model.BusCAN {
		s.canForm.Show()
		s.serialForm.Hide()
		return
	}
	s.canForm.Hide()
	s.serialForm.Show()
}

// update 根据当前项目与选中对象刷新候选节点与可发送状态。
func (s *sendPanel) update(project *model.Project, sel selection) {
	busID := sel.bus
	if sel.node != "" {
		if n := project.Node(sel.node); n != nil && n.Attached() {
			busID = n.Bus
		}
	}
	bus := project.Bus(busID)

	options := make([]string, 0, len(project.Nodes))
	s.nodes = map[string]model.NodeID{}
	var candidates []*model.Node
	if bus != nil {
		candidates = project.NodesOf(bus.ID)
		s.setKind(bus.Type)
	} else {
		for _, n := range project.Nodes {
			if n.Attached() {
				candidates = append(candidates, n)
			}
		}
	}
	for _, n := range candidates {
		label := n.Name
		if n.Role != model.RoleNode {
			label = fmt.Sprintf("%s（%s）", n.Name, n.Role.Label())
		}
		options = append(options, label)
		s.nodes[label] = n.ID
	}

	previous := s.nodeSelect.Selected
	s.nodeSelect.Options = options
	switch {
	case sel.node != "" && containsNode(candidates, sel.node):
		s.nodeSelect.SetSelected(labelOf(candidates, sel.node))
	case previous != "" && s.nodes[previous] != "":
		s.nodeSelect.SetSelected(previous)
	case len(options) > 0:
		s.nodeSelect.SetSelected(options[0])
	default:
		s.nodeSelect.ClearSelected()
	}
	s.nodeSelect.Refresh()

	s.sendable = false
	switch {
	case len(options) == 0:
		s.hint.SetText("先创建节点并连接到总线，才能发送数据")
	case bus == nil:
		s.hint.SetText("在画布上选择一条总线以确定发送目标")
	case !bus.Running:
		s.hint.SetText(fmt.Sprintf("总线 %s 未启动，请先在右上属性面板点击「启动」", bus.Name))
	default:
		s.sendable = true
		s.hint.SetText(fmt.Sprintf("目标总线：%s（%s，%s）", bus.Name, bus.Type.Label(), bus.Resource))
	}
	s.updateState()
}

func (s *sendPanel) updateState() {
	if s.sendable && s.nodeSelect.Selected != "" {
		s.sendButton.Enable()
		return
	}
	s.sendButton.Disable()
}

func (s *sendPanel) selectedNode() model.NodeID { return s.nodes[s.nodeSelect.Selected] }

func (s *sendPanel) submit() {
	node := s.selectedNode()
	if node == "" {
		s.fail(fmt.Errorf("请先选择发送节点"))
		return
	}
	frame, err := s.buildFrame()
	if err != nil {
		s.fail(err)
		return
	}
	if s.onSend != nil {
		s.onSend(node, frame)
	}
}

func (s *sendPanel) buildFrame() (model.Frame, error) {
	if s.kind == model.BusCAN {
		id, err := model.ParseCANID(s.idEntry.Text)
		if err != nil {
			return model.Frame{}, err
		}
		f := model.Frame{Kind: model.BusCAN, CANID: id, Ext: s.extCheck.Checked, RTR: s.rtrCheck.Checked}
		if !f.RTR {
			data, err := model.ParseHexBytes(s.dataEntry.Text)
			if err != nil {
				return model.Frame{}, err
			}
			f.Data = data
		}
		return f, f.Validate()
	}

	text := s.textEntry.Text
	var data []byte
	if s.formatSel.Selected == "Hex" {
		parsed, err := model.ParseHexBytes(text)
		if err != nil {
			return model.Frame{}, err
		}
		data = parsed
	} else {
		if strings.TrimSpace(text) == "" {
			return model.Frame{}, fmt.Errorf("发送内容为空")
		}
		data = []byte(text)
	}
	f := model.Frame{Kind: s.kind, Data: data}
	return f, f.Validate()
}

func (s *sendPanel) fail(err error) {
	if s.onError != nil {
		s.onError(err)
	}
}

func containsNode(nodes []*model.Node, id model.NodeID) bool {
	for _, n := range nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func labelOf(nodes []*model.Node, id model.NodeID) string {
	for _, n := range nodes {
		if n.ID != id {
			continue
		}
		if n.Role != model.RoleNode {
			return fmt.Sprintf("%s（%s）", n.Name, n.Role.Label())
		}
		return n.Name
	}
	return ""
}
