//go:build !linux

package serialbus

import "github.com/lansonsam/buslab/internal/model"

func newFactory(model.SerialBackend) PortFactory { return nil }
