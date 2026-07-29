package model

import (
	"errors"
	"fmt"
	"strings"
)

// Endpoint 数量约束（设计 §4.3）。
const MaxRS232Endpoints = 2

var (
	ErrBusNotFound  = errors.New("总线不存在")
	ErrNodeNotFound = errors.New("节点不存在")
)

func ValidateBus(b *Bus) error {
	if b == nil {
		return ErrBusNotFound
	}
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("总线名称不能为空")
	}
	if !b.Type.Valid() {
		return fmt.Errorf("未知总线类型 %q", b.Type)
	}
	if b.Type.IsSerial() {
		return b.Serial.Validate()
	}
	return nil
}

func ValidateNode(n *Node) error {
	if n == nil {
		return ErrNodeNotFound
	}
	if strings.TrimSpace(n.Name) == "" {
		return fmt.Errorf("节点名称不能为空")
	}
	if !n.Role.Valid() {
		return fmt.Errorf("未知节点角色 %q", n.Role)
	}
	return nil
}

// ValidateAttach 校验把 node 接到 bus 是否合法；role 为节点接入后的角色。
func ValidateAttach(p *Project, nodeID NodeID, busID BusID, role NodeRole) error {
	node := p.Node(nodeID)
	if node == nil {
		return ErrNodeNotFound
	}
	bus := p.Bus(busID)
	if bus == nil {
		return ErrBusNotFound
	}
	if !role.Valid() {
		return fmt.Errorf("未知节点角色 %q", role)
	}
	if node.Bus == busID {
		return fmt.Errorf("节点 %s 已连接到 %s", node.Name, bus.Name)
	}
	if node.Attached() {
		other := p.Bus(node.Bus)
		name := string(node.Bus)
		if other != nil {
			name = other.Name
		}
		return fmt.Errorf("节点 %s 已连接到 %s，请先断开", node.Name, name)
	}

	peers := p.NodesOf(busID)
	switch bus.Type {
	case BusRS232:
		if len(peers) >= MaxRS232Endpoints {
			return fmt.Errorf("RS-232 为点对点总线，最多 %d 个节点", MaxRS232Endpoints)
		}
	case BusRS422:
		if role == RoleMaster {
			for _, peer := range peers {
				if peer.Role == RoleMaster {
					return fmt.Errorf("RS-422 总线 %s 已存在主机 %s", bus.Name, peer.Name)
				}
			}
		}
	}
	return nil
}

// TopologyIssue 是不阻塞操作的提示（UI 以警告展示）。
type TopologyIssue struct {
	Bus     BusID
	Message string
}

// CheckBusReady 判断总线是否可以启动收发，返回致命错误；提示走 BusIssues。
func CheckBusReady(p *Project, busID BusID) error {
	bus := p.Bus(busID)
	if bus == nil {
		return ErrBusNotFound
	}
	nodes := p.NodesOf(busID)
	if len(nodes) == 0 {
		return fmt.Errorf("总线 %s 上没有节点", bus.Name)
	}
	switch bus.Type {
	case BusRS232:
		if len(nodes) > MaxRS232Endpoints {
			return fmt.Errorf("RS-232 总线 %s 上有 %d 个节点，超过 %d", bus.Name, len(nodes), MaxRS232Endpoints)
		}
	case BusRS422:
		masters := 0
		for _, n := range nodes {
			if n.Role == RoleMaster {
				masters++
			}
		}
		if masters != 1 {
			return fmt.Errorf("RS-422 总线 %s 需要恰好 1 个主机，当前 %d 个", bus.Name, masters)
		}
		if len(nodes) < 2 {
			return fmt.Errorf("RS-422 总线 %s 至少需要 1 个从机", bus.Name)
		}
	}
	return nil
}

// BusIssues 返回全部非致命拓扑提示。
func BusIssues(p *Project) []TopologyIssue {
	var out []TopologyIssue
	for _, b := range p.Buses {
		nodes := p.NodesOf(b.ID)
		switch b.Type {
		case BusCAN, BusRS485:
			if len(nodes) < 2 {
				out = append(out, TopologyIssue{Bus: b.ID,
					Message: fmt.Sprintf("%s 建议至少接入 2 个节点才能观察到收发", b.Name)})
			}
		case BusRS232:
			if len(nodes) < 2 {
				out = append(out, TopologyIssue{Bus: b.ID,
					Message: fmt.Sprintf("%s 需要 2 个节点构成点对点链路", b.Name)})
			}
		case BusRS422:
			if err := CheckBusReady(p, b.ID); err != nil {
				out = append(out, TopologyIssue{Bus: b.ID, Message: err.Error()})
			}
		}
	}
	return out
}

// ValidateProject 在加载项目文件后做一致性检查。
func ValidateProject(p *Project) error {
	if p == nil {
		return fmt.Errorf("项目为空")
	}
	seenBus := map[BusID]bool{}
	for _, b := range p.Buses {
		if b.ID == "" {
			return fmt.Errorf("存在缺少 ID 的总线")
		}
		if seenBus[b.ID] {
			return fmt.Errorf("总线 ID 重复：%s", b.ID)
		}
		seenBus[b.ID] = true
		if err := ValidateBus(b); err != nil {
			return err
		}
	}
	seenNode := map[NodeID]bool{}
	for _, n := range p.Nodes {
		if n.ID == "" {
			return fmt.Errorf("存在缺少 ID 的节点")
		}
		if seenNode[n.ID] {
			return fmt.Errorf("节点 ID 重复：%s", n.ID)
		}
		seenNode[n.ID] = true
		if err := ValidateNode(n); err != nil {
			return err
		}
		if n.Attached() && !seenBus[n.Bus] {
			return fmt.Errorf("节点 %s 引用了不存在的总线 %s", n.Name, n.Bus)
		}
	}
	for _, b := range p.Buses {
		nodes := p.NodesOf(b.ID)
		if b.Type == BusRS232 && len(nodes) > MaxRS232Endpoints {
			return fmt.Errorf("RS-232 总线 %s 上有 %d 个节点", b.Name, len(nodes))
		}
		if b.Type == BusRS422 {
			masters := 0
			for _, n := range nodes {
				if n.Role == RoleMaster {
					masters++
				}
			}
			if masters > 1 {
				return fmt.Errorf("RS-422 总线 %s 上有 %d 个主机", b.Name, masters)
			}
		}
	}
	return nil
}
