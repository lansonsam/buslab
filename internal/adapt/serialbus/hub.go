// Package serialbus 用虚拟 tty（tty0tty / pty）加用户态 Hub 提供
// RS-232 / RS-422 / RS-485 总线语义。注意：Linux 没有与 vcan 对等的内核虚拟
// 串行总线，多点广播由本包的 Hub 在应用层完成。
package serialbus

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/model"
)

// DefaultCollisionWindow 内两个不同节点先后发送即视为半双工冲突。
const DefaultCollisionWindow = 5 * time.Millisecond

const readBufferSize = 4096

// Port 是一个虚拟串口的 Hub 侧句柄；External 返回外部工具应打开的另一端。
type Port interface {
	io.ReadWriteCloser
	External() string
}

type member struct {
	node model.NodeID
	role model.NodeRole
	port Port
	done chan struct{}

	writeMu sync.Mutex
}

// Hub 按总线类型在各成员之间转发字节流。
type Hub struct {
	spec            model.BusSpec
	sink            adapt.Sink
	now             func() time.Time
	collisionWindow time.Duration

	mu       sync.Mutex
	members  map[model.NodeID]*member
	order    []model.NodeID
	closed   bool
	lastTxAt time.Time
	lastTxBy model.NodeID
}

func NewHub(spec model.BusSpec, sink adapt.Sink) *Hub {
	return &Hub{
		spec:            spec,
		sink:            sink,
		now:             time.Now,
		collisionWindow: DefaultCollisionWindow,
		members:         map[model.NodeID]*member{},
	}
}

func (h *Hub) Add(node model.NodeID, role model.NodeRole, port Port) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return adapt.ErrClosed
	}
	if _, ok := h.members[node]; ok {
		h.mu.Unlock()
		return fmt.Errorf("节点 %s 已接入该总线", node)
	}
	m := &member{node: node, role: role, port: port, done: make(chan struct{})}
	h.members[node] = m
	h.order = append(h.order, node)
	h.mu.Unlock()

	go h.readLoop(m)
	return nil
}

func (h *Hub) Remove(node model.NodeID) error {
	h.mu.Lock()
	m := h.members[node]
	delete(h.members, node)
	for i, id := range h.order {
		if id == node {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	h.mu.Unlock()
	if m == nil {
		return adapt.ErrNoEndpoint
	}
	err := m.port.Close()
	<-m.done
	return err
}

func (h *Hub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	members := make([]*member, 0, len(h.members))
	for _, m := range h.members {
		members = append(members, m)
	}
	h.members = map[model.NodeID]*member{}
	h.order = nil
	h.mu.Unlock()

	var firstErr error
	for _, m := range members {
		if err := m.port.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		<-m.done
	}
	return firstErr
}

// Send 由 UI 触发：把 data 当作 node 在总线上的一次发送。
func (h *Hub) Send(node model.NodeID, data []byte) error {
	return h.transmit(node, data)
}

func (h *Hub) readLoop(m *member) {
	defer close(m.done)
	buf := make([]byte, readBufferSize)
	for {
		n, err := m.port.Read(buf)
		if n > 0 {
			_ = h.transmit(m.node, append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			if !h.isClosing(m.node) && !errors.Is(err, io.EOF) {
				h.emit(m.node, model.DirRx, nil, fmt.Sprintf("读取 %s 失败：%v", m.port.External(), err))
			}
			return
		}
	}
}

func (h *Hub) isClosing(node model.NodeID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return true
	}
	_, ok := h.members[node]
	return !ok
}

func (h *Hub) transmit(src model.NodeID, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("发送内容为空")
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return adapt.ErrClosed
	}
	sender, ok := h.members[src]
	if !ok {
		h.mu.Unlock()
		return adapt.ErrNoEndpoint
	}
	note := h.collisionNoteLocked(src)
	h.lastTxAt = h.now()
	h.lastTxBy = src
	targets := h.targetsLocked(sender)
	h.mu.Unlock()

	h.emit(src, model.DirTx, data, note)

	var firstErr error
	for _, t := range targets {
		t.writeMu.Lock()
		_, err := t.port.Write(data)
		t.writeMu.Unlock()
		if err != nil {
			h.emit(t.node, model.DirRx, nil, fmt.Sprintf("写入 %s 失败：%v", t.port.External(), err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		h.emit(t.node, model.DirRx, data, "")
	}
	return firstErr
}

// collisionNoteLocked 仅对半双工的 RS-485 生效。
func (h *Hub) collisionNoteLocked(src model.NodeID) string {
	if h.spec.Type != model.BusRS485 || h.lastTxBy == "" || h.lastTxBy == src {
		return ""
	}
	if h.now().Sub(h.lastTxAt) < h.collisionWindow {
		return fmt.Sprintf("半双工冲突：与节点 %s 几乎同时发送", h.lastTxBy)
	}
	return ""
}

func (h *Hub) targetsLocked(sender *member) []*member {
	var out []*member
	for _, id := range h.order {
		m := h.members[id]
		if m == nil || m.node == sender.node {
			continue
		}
		switch h.spec.Type {
		case model.BusRS422:
			if sender.role == model.RoleMaster {
				if m.role != model.RoleMaster {
					out = append(out, m)
				}
			} else if m.role == model.RoleMaster {
				out = append(out, m)
			}
		default:
			out = append(out, m)
		}
	}
	return out
}

func (h *Hub) emit(node model.NodeID, dir model.Direction, data []byte, note string) {
	if h.sink == nil {
		return
	}
	h.sink(model.Frame{
		Time: h.now(),
		Bus:  h.spec.ID,
		Node: node,
		Dir:  dir,
		Kind: h.spec.Type,
		Data: data,
		Note: note,
	})
}

func (h *Hub) memberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.members)
}
