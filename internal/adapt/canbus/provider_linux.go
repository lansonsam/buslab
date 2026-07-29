//go:build linux

package canbus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/host"
	"github.com/lansonsam/buslab/internal/model"
)

// readTimeout 决定关闭 endpoint 时读循环最迟多久退出。
const readTimeout = 200 * time.Millisecond

type Provider struct {
	Host *host.Host
}

func NewProvider(h *host.Host) *Provider {
	if h == nil {
		h = host.New()
	}
	return &Provider{Host: h}
}

func (p *Provider) Supports(t model.BusType) bool { return t == model.BusCAN }

func (p *Provider) Create(ctx context.Context, spec model.BusSpec, sink adapt.Sink) (adapt.Bus, error) {
	if !p.Host.ModuleLoaded("vcan") {
		if err := p.Host.Modprobe(ctx, "vcan"); err != nil {
			return nil, err
		}
	}
	name, err := p.Host.AllocateVCANName(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.Host.AddVCAN(ctx, name); err != nil {
		return nil, err
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		_ = p.Host.DeleteLink(ctx, name)
		return nil, fmt.Errorf("接口 %s 创建后不可见：%w", name, err)
	}
	return &canBus{
		host:    p.Host,
		spec:    spec,
		ifname:  name,
		ifindex: iface.Index,
		sink:    sink,
		eps:     map[model.NodeID]*canEndpoint{},
	}, nil
}

type canBus struct {
	host    *host.Host
	spec    model.BusSpec
	ifname  string
	ifindex int
	sink    adapt.Sink

	mu     sync.Mutex
	closed bool
	eps    map[model.NodeID]*canEndpoint
}

func (b *canBus) Kind() model.BusType { return model.BusCAN }

func (b *canBus) Resource() string { return b.ifname }

func (b *canBus) Open(node model.NodeID, _ model.NodeRole) (adapt.Endpoint, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, adapt.ErrClosed
	}
	if _, ok := b.eps[node]; ok {
		b.mu.Unlock()
		return nil, fmt.Errorf("节点 %s 已在 %s 上有接入点", node, b.ifname)
	}
	b.mu.Unlock()

	fd, err := unix.Socket(unix.AF_CAN, unix.SOCK_RAW, unix.CAN_RAW)
	if err != nil {
		return nil, fmt.Errorf("创建 CAN 套接字失败：%w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrCAN{Ifindex: b.ifindex}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("绑定 %s 失败：%w", b.ifname, err)
	}
	tv := unix.NsecToTimeval(int64(readTimeout))
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("设置接收超时失败：%w", err)
	}

	ep := &canEndpoint{bus: b, node: node, fd: fd, done: make(chan struct{})}
	b.mu.Lock()
	b.eps[node] = ep
	b.mu.Unlock()

	go ep.readLoop()
	return ep, nil
}

func (b *canBus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	eps := make([]*canEndpoint, 0, len(b.eps))
	for _, ep := range b.eps {
		eps = append(eps, ep)
	}
	b.eps = map[model.NodeID]*canEndpoint{}
	b.mu.Unlock()

	var firstErr error
	for _, ep := range eps {
		if err := ep.shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.host.DeleteLink(ctx, b.ifname); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (b *canBus) forget(node model.NodeID) {
	b.mu.Lock()
	delete(b.eps, node)
	b.mu.Unlock()
}

func (b *canBus) emit(f model.Frame) {
	if b.sink != nil {
		b.sink(f)
	}
}

type canEndpoint struct {
	bus    *canBus
	node   model.NodeID
	fd     int
	closed atomic.Bool
	done   chan struct{}
	sendMu sync.Mutex
}

func (e *canEndpoint) Name() string { return e.bus.ifname }

func (e *canEndpoint) Node() model.NodeID { return e.node }

func (e *canEndpoint) Send(f model.Frame) error {
	if e.closed.Load() {
		return adapt.ErrClosed
	}
	raw, err := EncodeFrame(f)
	if err != nil {
		return err
	}
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	if _, err := unix.Write(e.fd, raw); err != nil {
		return fmt.Errorf("写 %s 失败：%w", e.bus.ifname, err)
	}
	return nil
}

func (e *canEndpoint) Close() error {
	err := e.shutdown()
	e.bus.forget(e.node)
	return err
}

func (e *canEndpoint) shutdown() error {
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	<-e.done
	return unix.Close(e.fd)
}

func (e *canEndpoint) readLoop() {
	defer close(e.done)
	buf := make([]byte, FrameSize)
	for !e.closed.Load() {
		n, err := unix.Read(e.fd, buf)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
				continue
			}
			if !e.closed.Load() {
				e.bus.emit(model.Frame{
					Time: time.Now(), Bus: e.bus.spec.ID, Node: e.node,
					Dir: model.DirRx, Kind: model.BusCAN,
					Note: fmt.Sprintf("读取失败：%v", err),
				})
			}
			return
		}
		f, derr := DecodeFrame(buf[:n])
		if derr != nil {
			continue
		}
		f.Time = time.Now()
		f.Bus = e.bus.spec.ID
		f.Node = e.node
		f.Dir = model.DirRx
		e.bus.emit(f)
	}
}
