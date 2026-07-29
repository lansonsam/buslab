// Package orch 是 UI 与总线后端之间的编排层：持有项目状态、校验拓扑、
// 管理内核资源生命周期，并把流量与状态变化以事件形式广播出去。
package orch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/model"
	"github.com/lansonsam/buslab/internal/persist"
)

type Orchestrator struct {
	registry *adapt.Registry
	broker   *broker

	mu      sync.Mutex
	project *model.Project
	report  model.HostReport
	buses   map[model.BusID]adapt.Bus
	eps     map[model.NodeID]adapt.Endpoint
	seq     uint64
	ids     int
	path    string
}

func New(registry *adapt.Registry, report model.HostReport) *Orchestrator {
	return &Orchestrator{
		registry: registry,
		broker:   newBroker(),
		project:  model.NewProject(""),
		report:   report,
		buses:    map[model.BusID]adapt.Bus{},
		eps:      map[model.NodeID]adapt.Endpoint{},
	}
}

func (o *Orchestrator) Subscribe(buffer int) (<-chan Event, func()) {
	return o.broker.subscribe(buffer)
}

// DroppedEvents 是订阅者消费不及时被丢弃的事件数，用于状态栏提示。
func (o *Orchestrator) DroppedEvents() uint64 { return o.broker.droppedCount() }

func (o *Orchestrator) HostReport() model.HostReport {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.report
}

func (o *Orchestrator) SetHostReport(r model.HostReport) {
	o.mu.Lock()
	o.report = r
	o.mu.Unlock()
	o.broker.publish(Event{Kind: EventHostStatus, Message: r.StatusLine()})
}

// Snapshot 返回项目的深拷贝，UI 只读渲染用。
func (o *Orchestrator) Snapshot() *model.Project {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snapshotLocked()
}

func (o *Orchestrator) snapshotLocked() *model.Project {
	out := &model.Project{Name: o.project.Name}
	for _, b := range o.project.Buses {
		out.Buses = append(out.Buses, b.Clone())
	}
	for _, n := range o.project.Nodes {
		out.Nodes = append(out.Nodes, n.Clone())
	}
	return out
}

func (o *Orchestrator) ProjectPath() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.path
}

func (o *Orchestrator) Supports(t model.BusType) bool { return o.registry.Supports(t) }

// CreateBus 只创建逻辑总线，内核资源在 StartBus 时申请。
func (o *Orchestrator) CreateBus(t model.BusType) (model.BusID, error) {
	if !t.Valid() {
		return "", fmt.Errorf("未知总线类型 %q", t)
	}
	o.mu.Lock()
	id := model.BusID(o.newIDLocked("bus"))
	bus := &model.Bus{
		ID:     id,
		Name:   o.project.NextName(t.Label() + " 总线"),
		Type:   t,
		Serial: model.DefaultSerialParams(),
		CAN:    model.DefaultCANParams(),
		Pos:    model.Point{X: 40, Y: float32(60 + 140*len(o.project.Buses))},
	}
	if err := model.ValidateBus(bus); err != nil {
		o.mu.Unlock()
		return "", err
	}
	o.project.AddBus(bus)
	o.mu.Unlock()

	o.broker.publish(Event{Kind: EventBusCreated, Bus: id, Message: bus.Name})
	return id, nil
}

func (o *Orchestrator) DeleteBus(id model.BusID) error {
	if err := o.StopBus(id); err != nil {
		return err
	}
	o.mu.Lock()
	bus := o.project.Bus(id)
	if bus == nil {
		o.mu.Unlock()
		return model.ErrBusNotFound
	}
	detached := o.project.NodesOf(id)
	nodeIDs := make([]model.NodeID, 0, len(detached))
	for _, n := range detached {
		nodeIDs = append(nodeIDs, n.ID)
	}
	o.project.RemoveBus(id)
	o.mu.Unlock()

	for _, n := range nodeIDs {
		o.broker.publish(Event{Kind: EventNodeChanged, Node: n})
	}
	o.broker.publish(Event{Kind: EventBusDeleted, Bus: id})
	return nil
}

func (o *Orchestrator) AddNode() (model.NodeID, error) {
	o.mu.Lock()
	id := model.NodeID(o.newIDLocked("node"))
	node := &model.Node{
		ID:   id,
		Name: o.project.NextName("节点"),
		Role: model.RoleNode,
		Pos:  model.Point{X: 320, Y: float32(60 + 70*len(o.project.Nodes))},
	}
	if err := model.ValidateNode(node); err != nil {
		o.mu.Unlock()
		return "", err
	}
	o.project.AddNode(node)
	o.mu.Unlock()

	o.broker.publish(Event{Kind: EventNodeChanged, Node: id, Message: node.Name})
	return id, nil
}

func (o *Orchestrator) DeleteNode(id model.NodeID) error {
	if err := o.DetachNode(id); err != nil && !errors.Is(err, errNotAttached) {
		return err
	}
	o.mu.Lock()
	if o.project.Node(id) == nil {
		o.mu.Unlock()
		return model.ErrNodeNotFound
	}
	o.project.RemoveNode(id)
	o.mu.Unlock()

	o.broker.publish(Event{Kind: EventNodeDeleted, Node: id})
	return nil
}

var errNotAttached = errors.New("节点未连接总线")

// AttachNode 把节点接到总线；若总线正在运行则立即申请接入点。
func (o *Orchestrator) AttachNode(node model.NodeID, bus model.BusID, role model.NodeRole) error {
	o.mu.Lock()
	if err := model.ValidateAttach(o.project, node, bus, role); err != nil {
		o.mu.Unlock()
		return err
	}
	n := o.project.Node(node)
	busName := o.project.Bus(bus).Name
	running := o.buses[bus]
	n.Bus = bus
	n.Role = role
	if running == nil {
		o.mu.Unlock()
		o.broker.publish(Event{Kind: EventNodeChanged, Node: node, Bus: bus})
		return nil
	}
	o.mu.Unlock()

	ep, err := running.Open(node, role)
	if err != nil {
		o.mu.Lock()
		n.Bus = ""
		n.Endpoint = ""
		o.mu.Unlock()
		o.broker.publish(Event{Kind: EventNodeChanged, Node: node})
		return fmt.Errorf("在 %s 上创建接入点失败：%w", busName, err)
	}
	o.mu.Lock()
	o.eps[node] = ep
	n.Endpoint = ep.Name()
	o.mu.Unlock()

	o.broker.publish(Event{Kind: EventNodeChanged, Node: node, Bus: bus})
	o.broker.publish(Event{Kind: EventBusChanged, Bus: bus})
	return nil
}

func (o *Orchestrator) DetachNode(node model.NodeID) error {
	o.mu.Lock()
	n := o.project.Node(node)
	if n == nil {
		o.mu.Unlock()
		return model.ErrNodeNotFound
	}
	if !n.Attached() {
		o.mu.Unlock()
		return errNotAttached
	}
	bus := n.Bus
	ep := o.eps[node]
	delete(o.eps, node)
	n.Bus = ""
	n.Endpoint = ""
	o.mu.Unlock()

	var err error
	if ep != nil {
		err = ep.Close()
	}
	o.broker.publish(Event{Kind: EventNodeChanged, Node: node})
	o.broker.publish(Event{Kind: EventBusChanged, Bus: bus})
	return err
}

// SetNodeRole 修改角色；RS-422 上角色影响转发方向，运行中会重建接入点。
func (o *Orchestrator) SetNodeRole(node model.NodeID, role model.NodeRole) error {
	if !role.Valid() {
		return fmt.Errorf("未知节点角色 %q", role)
	}
	o.mu.Lock()
	n := o.project.Node(node)
	if n == nil {
		o.mu.Unlock()
		return model.ErrNodeNotFound
	}
	if n.Role == role {
		o.mu.Unlock()
		return nil
	}
	if !n.Attached() {
		n.Role = role
		o.mu.Unlock()
		o.broker.publish(Event{Kind: EventNodeChanged, Node: node})
		return nil
	}
	bus, oldRole := n.Bus, n.Role
	// 先在副本上校验新角色，避免因校验失败而让节点停留在已断开状态。
	sim := o.snapshotLocked()
	sim.Node(node).Bus = ""
	if err := model.ValidateAttach(sim, node, bus, role); err != nil {
		o.mu.Unlock()
		return err
	}
	o.mu.Unlock()

	if err := o.DetachNode(node); err != nil {
		return err
	}
	if err := o.AttachNode(node, bus, role); err != nil {
		return errors.Join(err, o.AttachNode(node, bus, oldRole))
	}
	return nil
}

func (o *Orchestrator) RenameBus(id model.BusID, name string) error {
	o.mu.Lock()
	b := o.project.Bus(id)
	if b == nil {
		o.mu.Unlock()
		return model.ErrBusNotFound
	}
	old := b.Name
	b.Name = strings.TrimSpace(name)
	if err := model.ValidateBus(b); err != nil {
		b.Name = old
		o.mu.Unlock()
		return err
	}
	o.mu.Unlock()
	o.broker.publish(Event{Kind: EventBusChanged, Bus: id})
	return nil
}

func (o *Orchestrator) RenameNode(id model.NodeID, name string) error {
	o.mu.Lock()
	n := o.project.Node(id)
	if n == nil {
		o.mu.Unlock()
		return model.ErrNodeNotFound
	}
	old := n.Name
	n.Name = strings.TrimSpace(name)
	if err := model.ValidateNode(n); err != nil {
		n.Name = old
		o.mu.Unlock()
		return err
	}
	o.mu.Unlock()
	o.broker.publish(Event{Kind: EventNodeChanged, Node: id})
	return nil
}

func (o *Orchestrator) SetSerialParams(id model.BusID, params model.SerialParams) error {
	if err := params.Validate(); err != nil {
		return err
	}
	o.mu.Lock()
	b := o.project.Bus(id)
	if b == nil {
		o.mu.Unlock()
		return model.ErrBusNotFound
	}
	if !b.Type.IsSerial() {
		o.mu.Unlock()
		return fmt.Errorf("%s 不是串口总线", b.Name)
	}
	if b.Running {
		o.mu.Unlock()
		return fmt.Errorf("请先停止 %s 再修改串口参数", b.Name)
	}
	b.Serial = params
	o.mu.Unlock()
	o.broker.publish(Event{Kind: EventBusChanged, Bus: id})
	return nil
}

func (o *Orchestrator) SetCANParams(id model.BusID, params model.CANParams) error {
	o.mu.Lock()
	b := o.project.Bus(id)
	if b == nil {
		o.mu.Unlock()
		return model.ErrBusNotFound
	}
	if b.Type != model.BusCAN {
		o.mu.Unlock()
		return fmt.Errorf("%s 不是 CAN 总线", b.Name)
	}
	b.CAN = params
	o.mu.Unlock()
	o.broker.publish(Event{Kind: EventBusChanged, Bus: id})
	return nil
}

// SetBusPos / SetNodePos 供画布拖拽使用，不产生事件以免与拖拽互相打断。
func (o *Orchestrator) SetBusPos(id model.BusID, p model.Point) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if b := o.project.Bus(id); b != nil {
		b.Pos = p
	}
}

func (o *Orchestrator) SetNodePos(id model.NodeID, p model.Point) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if n := o.project.Node(id); n != nil {
		n.Pos = p
	}
}

// StartBus 申请内核资源并为已连接节点打开接入点，任一步失败则整体回滚。
func (o *Orchestrator) StartBus(ctx context.Context, id model.BusID) error {
	o.mu.Lock()
	b := o.project.Bus(id)
	if b == nil {
		o.mu.Unlock()
		return model.ErrBusNotFound
	}
	if b.Running {
		o.mu.Unlock()
		return nil
	}
	if err := model.CheckBusReady(o.project, id); err != nil {
		o.mu.Unlock()
		return err
	}
	spec := b.Spec()
	type member struct {
		id   model.NodeID
		role model.NodeRole
	}
	members := make([]member, 0)
	for _, n := range o.project.NodesOf(id) {
		members = append(members, member{id: n.ID, role: n.Role})
	}
	o.mu.Unlock()

	bus, err := o.registry.Create(ctx, spec, o.onFrame)
	if err != nil {
		return fmt.Errorf("启动 %s 失败：%w", spec.Name, err)
	}

	opened := make(map[model.NodeID]adapt.Endpoint, len(members))
	for _, m := range members {
		ep, err := bus.Open(m.id, m.role)
		if err != nil {
			for _, e := range opened {
				_ = e.Close()
			}
			_ = bus.Close()
			return fmt.Errorf("启动 %s 失败：%w", spec.Name, err)
		}
		opened[m.id] = ep
	}

	resource := bus.Resource()
	o.mu.Lock()
	o.buses[id] = bus
	b.Running = true
	b.Resource = resource
	for nodeID, ep := range opened {
		o.eps[nodeID] = ep
		if n := o.project.Node(nodeID); n != nil {
			n.Endpoint = ep.Name()
		}
	}
	o.mu.Unlock()

	o.broker.publish(Event{Kind: EventBusChanged, Bus: id, Message: "已启动 " + resource})
	for nodeID := range opened {
		o.broker.publish(Event{Kind: EventNodeChanged, Node: nodeID, Bus: id})
	}
	return nil
}

func (o *Orchestrator) StopBus(id model.BusID) error {
	o.mu.Lock()
	b := o.project.Bus(id)
	if b == nil {
		o.mu.Unlock()
		return model.ErrBusNotFound
	}
	bus := o.buses[id]
	if bus == nil {
		b.Running = false
		b.Resource = ""
		o.mu.Unlock()
		return nil
	}
	delete(o.buses, id)
	var nodeIDs []model.NodeID
	for _, n := range o.project.NodesOf(id) {
		delete(o.eps, n.ID)
		n.Endpoint = ""
		nodeIDs = append(nodeIDs, n.ID)
	}
	b.Running = false
	b.Resource = ""
	o.mu.Unlock()

	// Bus.Close 会关闭其全部接入点，无需逐个关闭。
	err := bus.Close()
	for _, n := range nodeIDs {
		o.broker.publish(Event{Kind: EventNodeChanged, Node: n, Bus: id})
	}
	o.broker.publish(Event{Kind: EventBusChanged, Bus: id, Message: "已停止"})
	if err != nil {
		o.broker.publish(Event{Kind: EventError, Bus: id, Err: err, Message: "释放资源时出错"})
	}
	return err
}

// StopAll 供退出钩子调用，尽力释放所有资源。
func (o *Orchestrator) StopAll() error {
	o.mu.Lock()
	ids := make([]model.BusID, 0, len(o.buses))
	for id := range o.buses {
		ids = append(ids, id)
	}
	o.mu.Unlock()

	var errs []error
	for _, id := range ids {
		if err := o.StopBus(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *Orchestrator) SendFrame(node model.NodeID, f model.Frame) error {
	o.mu.Lock()
	n := o.project.Node(node)
	if n == nil {
		o.mu.Unlock()
		return model.ErrNodeNotFound
	}
	if !n.Attached() {
		o.mu.Unlock()
		return fmt.Errorf("节点 %s 未连接任何总线", n.Name)
	}
	bus := o.project.Bus(n.Bus)
	busID, busName, busType := n.Bus, bus.Name, bus.Type
	ep := o.eps[node]
	o.mu.Unlock()

	if ep == nil {
		return fmt.Errorf("总线 %s 未启动，无法发送", busName)
	}
	f.Bus = busID
	f.Node = node
	f.Kind = busType
	if err := f.Validate(); err != nil {
		return err
	}
	return ep.Send(f)
}

// onFrame 是所有 Adapter 的统一入口，负责补齐序号并广播。
func (o *Orchestrator) onFrame(f model.Frame) {
	o.mu.Lock()
	o.seq++
	f.Seq = o.seq
	o.mu.Unlock()
	o.broker.publish(Event{Kind: EventFrame, Bus: f.Bus, Node: f.Node, Frame: f})
}

func (o *Orchestrator) Save(path string) error {
	path = persist.EnsureExtension(path)
	o.mu.Lock()
	snapshot := o.snapshotLocked()
	o.mu.Unlock()
	if err := persist.Save(path, snapshot); err != nil {
		return err
	}
	o.mu.Lock()
	o.path = path
	o.mu.Unlock()
	return nil
}

// Load 会先停止全部总线，再用文件内容替换当前项目。
func (o *Orchestrator) Load(path string) error {
	project, err := persist.Load(path)
	if err != nil {
		return err
	}
	if err := o.StopAll(); err != nil {
		return err
	}
	o.mu.Lock()
	o.project = project
	o.path = path
	o.ids = 0
	o.mu.Unlock()

	o.broker.publish(Event{Kind: EventProjectReplaced, Message: project.Name})
	return nil
}

func (o *Orchestrator) Reset() error {
	if err := o.StopAll(); err != nil {
		return err
	}
	o.mu.Lock()
	o.project = model.NewProject("")
	o.path = ""
	o.ids = 0
	o.mu.Unlock()
	o.broker.publish(Event{Kind: EventProjectReplaced, Message: "新建实验"})
	return nil
}

// Close 释放资源并关闭事件通道，仅在退出时调用。
func (o *Orchestrator) Close() error {
	err := o.StopAll()
	o.broker.close()
	return err
}

func (o *Orchestrator) newIDLocked(prefix string) string {
	for {
		o.ids++
		candidate := fmt.Sprintf("%s%d", prefix, o.ids)
		if o.project.Bus(model.BusID(candidate)) == nil && o.project.Node(model.NodeID(candidate)) == nil {
			return candidate
		}
	}
}
