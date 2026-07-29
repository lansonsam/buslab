// Package host 封装对宿主机的命令调用（modprobe / ip）与能力探测。
package host

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/lansonsam/buslab/internal/model"
)

type Kind int

const (
	KindOther Kind = iota
	KindPermission
	KindMissingCommand
	KindMissingModule
	KindUnsupported
	KindTimeout
)

type Error struct {
	Op     string
	Kind   Kind
	Detail string
	Err    error
}

func (e *Error) Error() string {
	msg := e.Op
	switch e.Kind {
	case KindPermission:
		msg += "：权限不足（需要 root 或 CAP_NET_ADMIN）"
	case KindMissingCommand:
		msg += "：命令不存在"
	case KindMissingModule:
		msg += "：内核模块不可用"
	case KindUnsupported:
		msg += "：当前平台不支持"
	case KindTimeout:
		msg += "：执行超时"
	default:
		msg += "：执行失败"
	}
	if e.Detail != "" {
		msg += "（" + strings.TrimSpace(e.Detail) + "）"
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

func KindOf(err error) Kind {
	var he *Error
	if errors.As(err, &he) {
		return he.Kind
	}
	return KindOther
}

var ErrUnsupported = &Error{Op: "宿主操作", Kind: KindUnsupported}

type Result struct {
	Stdout string
	Stderr string
	Code   int
}

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
	LookPath(name string) bool
}

// ExecRunner 通过 os/exec 执行真实命令。
type ExecRunner struct {
	Timeout time.Duration
}

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) (Result, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr strings.Builder
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if cmd.ProcessState != nil {
		res.Code = cmd.ProcessState.ExitCode()
	}
	return res, err
}

func (r ExecRunner) LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

type Host struct {
	Runner Runner
}

func New() *Host { return &Host{Runner: ExecRunner{Timeout: 5 * time.Second}} }

func (h *Host) runner() Runner {
	if h.Runner == nil {
		h.Runner = ExecRunner{Timeout: 5 * time.Second}
	}
	return h.Runner
}

func (h *Host) run(ctx context.Context, op, name string, args ...string) (Result, error) {
	res, err := h.runner().Run(ctx, name, args...)
	if err == nil {
		return res, nil
	}
	return res, classify(op, name, res, err)
}

func classify(op, name string, res Result, err error) *Error {
	detail := strings.TrimSpace(res.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(res.Stdout)
	}
	kind := KindOther
	lower := strings.ToLower(detail)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = KindTimeout
	case errors.Is(err, exec.ErrNotFound):
		kind = KindMissingCommand
		if detail == "" {
			detail = name
		}
	case strings.Contains(lower, "not permitted"),
		strings.Contains(lower, "permission denied"),
		strings.Contains(lower, "must be superuser"),
		strings.Contains(lower, "are you root"):
		kind = KindPermission
	case strings.Contains(lower, "module") && strings.Contains(lower, "not found"),
		strings.Contains(lower, "unknown device type"),
		strings.Contains(lower, "does not support"):
		kind = KindMissingModule
	}
	if detail == "" {
		detail = err.Error()
	}
	return &Error{Op: op, Kind: kind, Detail: detail, Err: err}
}

// Modprobe 加载内核模块；已加载时 modprobe 自身是幂等的。
func (h *Host) Modprobe(ctx context.Context, module string) error {
	if !h.runner().LookPath("modprobe") {
		return &Error{Op: "加载模块 " + module, Kind: KindMissingCommand, Detail: "modprobe"}
	}
	_, err := h.run(ctx, "加载模块 "+module, "modprobe", module)
	return err
}

// Detect 探测宿主机能力。
func (h *Host) Detect(ctx context.Context) model.HostReport {
	report := model.HostReport{OS: platformName(), SerialBackend: model.SerialBackendNone}
	probePlatform(ctx, h, &report)
	return report
}

func vcanIfaceName(index int) string {
	name := fmt.Sprintf("vcanbl%d", index)
	if len(name) > 15 {
		name = name[:15]
	}
	return name
}
