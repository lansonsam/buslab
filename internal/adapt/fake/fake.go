// Package fake 提供纯内存的总线后端，用于无内核环境下的测试与界面预览。
package fake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/adapt/serialbus"
	"github.com/lansonsam/buslab/internal/model"
)

// Registry 返回一个 CAN + 串口全支持的内存后端集合。
func Registry() *adapt.Registry {
	return adapt.NewRegistry(&CANProvider{}, serialbus.NewProviderWithFactory(&MemFactory{}))
}

// CANProvider 用内存广播模拟 vcan。
type CANProvider struct {
	mu  sync.Mutex
	seq int
}

func (p *CANProvider) Supports(t model.BusType) bool { return t == model.BusCAN }

func (p *CANProvider) Create(_ context.Context, spec model.BusSpec, sink adapt.Sink) (adapt.Bus, error) {
	p.mu.Lock()
	p.seq++
	name := fmt.Sprintf("memcan%d", p.seq)
	p.mu.Unlock()
	return &canBus{spec: spec, sink: sink, name: name, eps: map[model.NodeID]*canEndpoint{}}, nil
}

type canBus struct {
	spec model.BusSpec
	sink adapt.Sink
	name string

	mu     sync.Mutex
	closed bool
	eps    map[model.NodeID]*canEndpoint
}

func (b *canBus) Kind() model.BusType { return model.BusCAN }

func (b *canBus) Resource() string { return b.name }

func (b *canBus) Open(node model.NodeID, _ model.NodeRole) (adapt.Endpoint, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, adapt.ErrClosed
	}
	if _, ok := b.eps[node]; ok {
		return nil, fmt.Errorf("节点 %s 已接入", node)
	}
	ep := &canEndpoint{bus: b, node: node}
	b.eps[node] = ep
	return ep, nil
}

func (b *canBus) Close() error {
	b.mu.Lock()
	b.closed = true
	b.eps = map[model.NodeID]*canEndpoint{}
	b.mu.Unlock()
	return nil
}

func (b *canBus) emit(f model.Frame) {
	if b.sink != nil {
		b.sink(f)
	}
}

type canEndpoint struct {
	bus    *canBus
	node   model.NodeID
	closed bool
}

func (e *canEndpoint) Name() string { return e.bus.name }

func (e *canEndpoint) Node() model.NodeID { return e.node }

func (e *canEndpoint) Send(f model.Frame) error {
	if err := f.Validate(); err != nil {
		return err
	}
	e.bus.mu.Lock()
	if e.closed || e.bus.closed {
		e.bus.mu.Unlock()
		return adapt.ErrClosed
	}
	peers := make([]model.NodeID, 0, len(e.bus.eps))
	for id := range e.bus.eps {
		if id != e.node {
			peers = append(peers, id)
		}
	}
	e.bus.mu.Unlock()

	now := time.Now()
	tx := f
	tx.Time, tx.Bus, tx.Node, tx.Dir, tx.Kind = now, e.bus.spec.ID, e.node, model.DirTx, model.BusCAN
	e.bus.emit(tx)
	for _, id := range peers {
		rx := f
		rx.Time, rx.Bus, rx.Node, rx.Dir, rx.Kind = now, e.bus.spec.ID, id, model.DirRx, model.BusCAN
		e.bus.emit(rx)
	}
	return nil
}

func (e *canEndpoint) Close() error {
	e.bus.mu.Lock()
	e.closed = true
	delete(e.bus.eps, e.node)
	e.bus.mu.Unlock()
	return nil
}

// MemFactory 为串口总线提供内存端口，External 名称仅用于展示。
type MemFactory struct {
	mu    sync.Mutex
	seq   int
	ports map[model.NodeID]*MemPort
}

func (f *MemFactory) Backend() model.SerialBackend { return model.SerialBackendNone }

func (f *MemFactory) Open(_ model.BusSpec, node model.NodeID) (serialbus.Port, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	p := &MemPort{
		external: fmt.Sprintf("mem://tty%d", f.seq),
		in:       make(chan []byte, 32),
		closed:   make(chan struct{}),
	}
	if f.ports == nil {
		f.ports = map[model.NodeID]*MemPort{}
	}
	f.ports[node] = p
	return p, nil
}

// Port 返回某节点的内存端口，便于测试注入或读取外部侧数据。
func (f *MemFactory) Port(node model.NodeID) *MemPort {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ports[node]
}

// MemPort 模拟一个虚拟串口的 Hub 侧；外部侧数据保存在内存缓冲中。
type MemPort struct {
	external string
	in       chan []byte
	closed   chan struct{}
	once     sync.Once

	mu       sync.Mutex
	received [][]byte
}

func (p *MemPort) Read(b []byte) (int, error) {
	select {
	case data := <-p.in:
		return copy(b, data), nil
	case <-p.closed:
		return 0, adapt.ErrClosed
	}
}

func (p *MemPort) Write(b []byte) (int, error) {
	select {
	case <-p.closed:
		return 0, adapt.ErrClosed
	default:
	}
	p.mu.Lock()
	p.received = append(p.received, append([]byte(nil), b...))
	if len(p.received) > 256 {
		p.received = p.received[1:]
	}
	p.mu.Unlock()
	return len(b), nil
}

func (p *MemPort) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func (p *MemPort) External() string { return p.external }

// Inject 模拟外部程序向该端口写入数据。
func (p *MemPort) Inject(data []byte) {
	select {
	case p.in <- append([]byte(nil), data...):
	case <-p.closed:
	}
}

// Received 返回外部侧收到的数据块副本。
func (p *MemPort) Received() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]byte(nil), p.received...)
}
