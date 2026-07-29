// Package model 定义虚拟总线实验室的纯数据结构与拓扑规则，不依赖任何其它内部包。
package model

import (
	"fmt"
	"strings"
)

type (
	BusID  string
	NodeID string
)

type BusType string

const (
	BusCAN   BusType = "can"
	BusRS232 BusType = "rs232"
	BusRS422 BusType = "rs422"
	BusRS485 BusType = "rs485"
)

var AllBusTypes = []BusType{BusCAN, BusRS232, BusRS422, BusRS485}

func (t BusType) Valid() bool {
	switch t {
	case BusCAN, BusRS232, BusRS422, BusRS485:
		return true
	}
	return false
}

func (t BusType) IsSerial() bool {
	switch t {
	case BusRS232, BusRS422, BusRS485:
		return true
	}
	return false
}

func (t BusType) Label() string {
	switch t {
	case BusCAN:
		return "CAN"
	case BusRS232:
		return "RS-232"
	case BusRS422:
		return "RS-422"
	case BusRS485:
		return "RS-485"
	}
	return string(t)
}

type NodeRole string

const (
	RoleNode   NodeRole = "node"
	RoleMaster NodeRole = "master"
	RoleSlave  NodeRole = "slave"
)

func (r NodeRole) Valid() bool {
	switch r {
	case RoleNode, RoleMaster, RoleSlave:
		return true
	}
	return false
}

func (r NodeRole) Label() string {
	switch r {
	case RoleMaster:
		return "主机"
	case RoleSlave:
		return "从机"
	}
	return "节点"
}

type Parity string

const (
	ParityNone Parity = "none"
	ParityEven Parity = "even"
	ParityOdd  Parity = "odd"
)

var AllParities = []Parity{ParityNone, ParityEven, ParityOdd}

func (p Parity) Valid() bool {
	switch p {
	case ParityNone, ParityEven, ParityOdd:
		return true
	}
	return false
}

// SerialParams 是串口线路参数。回退到 pty 时波特率等仅作记录，不影响实际吞吐。
type SerialParams struct {
	BaudRate int    `json:"baudRate"`
	DataBits int    `json:"dataBits"`
	Parity   Parity `json:"parity"`
	StopBits int    `json:"stopBits"`
}

func DefaultSerialParams() SerialParams {
	return SerialParams{BaudRate: 115200, DataBits: 8, Parity: ParityNone, StopBits: 1}
}

var StandardBaudRates = []int{1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600}

func (p SerialParams) Validate() error {
	if p.BaudRate <= 0 {
		return fmt.Errorf("波特率必须为正数，得到 %d", p.BaudRate)
	}
	switch p.DataBits {
	case 5, 6, 7, 8:
	default:
		return fmt.Errorf("数据位只支持 5/6/7/8，得到 %d", p.DataBits)
	}
	if !p.Parity.Valid() {
		return fmt.Errorf("校验位无效：%q", p.Parity)
	}
	switch p.StopBits {
	case 1, 2:
	default:
		return fmt.Errorf("停止位只支持 1/2，得到 %d", p.StopBits)
	}
	return nil
}

func (p SerialParams) String() string {
	parity := map[Parity]string{ParityNone: "N", ParityEven: "E", ParityOdd: "O"}[p.Parity]
	if parity == "" {
		parity = "?"
	}
	return fmt.Sprintf("%d %d%s%d", p.BaudRate, p.DataBits, parity, p.StopBits)
}

// CANParams 中的 Bitrate 对 vcan 无实际作用，仅用于界面展示与项目记录。
type CANParams struct {
	Bitrate int  `json:"bitrate"`
	FD      bool `json:"fd"`
}

func DefaultCANParams() CANParams { return CANParams{Bitrate: 500000} }

type Point struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
}

type Bus struct {
	ID     BusID        `json:"id"`
	Name   string       `json:"name"`
	Type   BusType      `json:"type"`
	Serial SerialParams `json:"serial"`
	CAN    CANParams    `json:"can"`
	Pos    Point        `json:"pos"`

	// Resource 是运行期的内核资源名（vcanbl1、/dev/tnt0 …），不参与持久化。
	Resource string `json:"-"`
	Running  bool   `json:"-"`
}

func (b *Bus) Clone() *Bus {
	c := *b
	return &c
}

type Node struct {
	ID   NodeID   `json:"id"`
	Name string   `json:"name"`
	Bus  BusID    `json:"bus"`
	Role NodeRole `json:"role"`
	Pos  Point    `json:"pos"`

	// Endpoint 是运行期分配的接入点名称，不参与持久化。
	Endpoint string `json:"-"`
}

func (n *Node) Attached() bool { return n.Bus != "" }

func (n *Node) Clone() *Node {
	c := *n
	return &c
}

// BusSpec 是 Adapter 创建总线所需的最小信息。
type BusSpec struct {
	ID     BusID
	Name   string
	Type   BusType
	Serial SerialParams
	CAN    CANParams
}

func (b *Bus) Spec() BusSpec {
	return BusSpec{ID: b.ID, Name: b.Name, Type: b.Type, Serial: b.Serial, CAN: b.CAN}
}

type Project struct {
	Name  string  `json:"name"`
	Buses []*Bus  `json:"buses"`
	Nodes []*Node `json:"nodes"`
}

func NewProject(name string) *Project {
	if strings.TrimSpace(name) == "" {
		name = "未命名实验"
	}
	return &Project{Name: name}
}

func (p *Project) Bus(id BusID) *Bus {
	for _, b := range p.Buses {
		if b.ID == id {
			return b
		}
	}
	return nil
}

func (p *Project) Node(id NodeID) *Node {
	for _, n := range p.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func (p *Project) NodesOf(id BusID) []*Node {
	var out []*Node
	for _, n := range p.Nodes {
		if n.Bus == id {
			out = append(out, n)
		}
	}
	return out
}

func (p *Project) AddBus(b *Bus) { p.Buses = append(p.Buses, b) }

func (p *Project) AddNode(n *Node) { p.Nodes = append(p.Nodes, n) }

// RemoveBus 删除总线，并把其上的节点置为未连接。
func (p *Project) RemoveBus(id BusID) {
	buses := p.Buses[:0]
	for _, b := range p.Buses {
		if b.ID != id {
			buses = append(buses, b)
		}
	}
	p.Buses = buses
	for _, n := range p.Nodes {
		if n.Bus == id {
			n.Bus = ""
			n.Endpoint = ""
		}
	}
}

func (p *Project) RemoveNode(id NodeID) {
	nodes := p.Nodes[:0]
	for _, n := range p.Nodes {
		if n.ID != id {
			nodes = append(nodes, n)
		}
	}
	p.Nodes = nodes
}

// NextName 生成形如 "CAN 总线 2" 的不重名名称。
func (p *Project) NextName(prefix string) string {
	used := make(map[string]bool, len(p.Buses)+len(p.Nodes))
	for _, b := range p.Buses {
		used[b.Name] = true
	}
	for _, n := range p.Nodes {
		used[n.Name] = true
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s %d", prefix, i)
		if !used[name] {
			return name
		}
	}
}
