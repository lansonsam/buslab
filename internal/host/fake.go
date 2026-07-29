package host

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// FakeRunner 供无内核环境的测试使用。
type FakeRunner struct {
	mu       sync.Mutex
	calls    []string
	NotFound map[string]bool
	// Handler 可按命令返回自定义结果；返回 handled=false 表示走默认成功路径。
	Handler func(name string, args []string) (res Result, err error, handled bool)
	// Links 是 ip link show 的模拟输出接口列表。
	Links []string
}

func (f *FakeRunner) Run(_ context.Context, name string, args ...string) (Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	f.mu.Unlock()

	if f.Handler != nil {
		if res, err, handled := f.Handler(name, args); handled {
			return res, err
		}
	}
	if !f.LookPath(name) {
		return Result{Code: -1}, exec.ErrNotFound
	}
	if name == "ip" && len(args) >= 2 && args[len(args)-2] == "link" && args[len(args)-1] == "show" {
		var sb strings.Builder
		for i, l := range f.Links {
			fmt.Fprintf(&sb, "%d: %s: <NOARP,UP> mtu 16\n", i+1, l)
		}
		return Result{Stdout: sb.String()}, nil
	}
	return Result{}, nil
}

func (f *FakeRunner) LookPath(name string) bool {
	return !f.NotFound[name]
}

func (f *FakeRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
