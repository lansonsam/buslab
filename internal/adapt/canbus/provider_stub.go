//go:build !linux

package canbus

import (
	"context"

	"github.com/lansonsam/buslab/internal/adapt"
	"github.com/lansonsam/buslab/internal/host"
	"github.com/lansonsam/buslab/internal/model"
)

type Provider struct {
	Host *host.Host
}

func NewProvider(h *host.Host) *Provider { return &Provider{Host: h} }

func (p *Provider) Supports(t model.BusType) bool { return t == model.BusCAN }

func (p *Provider) Create(context.Context, model.BusSpec, adapt.Sink) (adapt.Bus, error) {
	return nil, adapt.ErrUnsupported
}
