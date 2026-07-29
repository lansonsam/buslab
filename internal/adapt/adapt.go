// Package adapt 定义总线后端（Adapter）的公共接口。
// 上层只依赖这些接口，从而可以用内存 fake 替换真实内核资源。
package adapt

import (
	"context"
	"errors"

	"github.com/lansonsam/buslab/internal/model"
)

var (
	ErrUnsupported = errors.New("当前平台不支持该总线后端")
	ErrClosed      = errors.New("总线已关闭")
	ErrNoEndpoint  = errors.New("节点未接入总线")
)

// Sink 接收 Adapter 产生的流量事件（通常来自读循环 goroutine）。
type Sink func(model.Frame)

type Endpoint interface {
	// Name 是用户可见的接入点资源名，如 vcanbl1 或 /dev/tnt0。
	Name() string
	Node() model.NodeID
	Send(model.Frame) error
	Close() error
}

type Bus interface {
	Kind() model.BusType
	// Resource 是用户可见的总线资源描述，如 vcanbl1 或 tty0tty。
	Resource() string
	Open(node model.NodeID, role model.NodeRole) (Endpoint, error)
	Close() error
}

type Provider interface {
	Supports(model.BusType) bool
	Create(ctx context.Context, spec model.BusSpec, sink Sink) (Bus, error)
}

// Registry 按总线类型分发到具体 Provider。
type Registry struct {
	providers []Provider
}

func NewRegistry(providers ...Provider) *Registry {
	return &Registry{providers: providers}
}

func (r *Registry) Create(ctx context.Context, spec model.BusSpec, sink Sink) (Bus, error) {
	for _, p := range r.providers {
		if p.Supports(spec.Type) {
			return p.Create(ctx, spec, sink)
		}
	}
	return nil, ErrUnsupported
}

func (r *Registry) Supports(t model.BusType) bool {
	for _, p := range r.providers {
		if p.Supports(t) {
			return true
		}
	}
	return false
}
