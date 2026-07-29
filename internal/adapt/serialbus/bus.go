package serialbus

import (
	"context"
	"fmt"
	"sync"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/model"
)

// PortFactory 负责分配虚拟串口对：返回 Hub 侧句柄，External 为外部工具可打开的一端。
type PortFactory interface {
	Backend() model.SerialBackend
	Open(spec model.BusSpec, node model.NodeID) (Port, error)
}

type Provider struct {
	factory PortFactory
}

// NewProvider 依据探测到的后端创建 Provider；平台不支持时 factory 为 nil。
func NewProvider(backend model.SerialBackend) *Provider {
	return &Provider{factory: newFactory(backend)}
}

func NewProviderWithFactory(f PortFactory) *Provider { return &Provider{factory: f} }

func (p *Provider) Supports(t model.BusType) bool { return t.IsSerial() }

func (p *Provider) Backend() model.SerialBackend {
	if p.factory == nil {
		return model.SerialBackendNone
	}
	return p.factory.Backend()
}

func (p *Provider) Create(_ context.Context, spec model.BusSpec, sink adapt.Sink) (adapt.Bus, error) {
	if p.factory == nil {
		return nil, adapt.ErrUnsupported
	}
	if !spec.Type.IsSerial() {
		return nil, adapt.ErrUnsupported
	}
	return &serialBus{spec: spec, factory: p.factory, hub: NewHub(spec, sink)}, nil
}

type serialBus struct {
	spec    model.BusSpec
	factory PortFactory
	hub     *Hub

	mu     sync.Mutex
	closed bool
}

func (b *serialBus) Kind() model.BusType { return b.spec.Type }

func (b *serialBus) Resource() string {
	return fmt.Sprintf("%s · %d 端口", b.factory.Backend(), b.hub.memberCount())
}

func (b *serialBus) Open(node model.NodeID, role model.NodeRole) (adapt.Endpoint, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, adapt.ErrClosed
	}
	b.mu.Unlock()

	if b.spec.Type == model.BusRS232 && b.hub.memberCount() >= model.MaxRS232Endpoints {
		return nil, fmt.Errorf("RS-232 总线最多 %d 个接入点", model.MaxRS232Endpoints)
	}
	port, err := b.factory.Open(b.spec, node)
	if err != nil {
		return nil, err
	}
	if err := b.hub.Add(node, role, port); err != nil {
		_ = port.Close()
		return nil, err
	}
	return &serialEndpoint{bus: b, node: node, external: port.External()}, nil
}

func (b *serialBus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	return b.hub.Close()
}

type serialEndpoint struct {
	bus      *serialBus
	node     model.NodeID
	external string
}

func (e *serialEndpoint) Name() string { return e.external }

func (e *serialEndpoint) Node() model.NodeID { return e.node }

func (e *serialEndpoint) Send(f model.Frame) error {
	if err := f.Validate(); err != nil {
		return err
	}
	return e.bus.hub.Send(e.node, f.Data)
}

func (e *serialEndpoint) Close() error { return e.bus.hub.Remove(e.node) }
