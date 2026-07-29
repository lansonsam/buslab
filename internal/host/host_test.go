package host

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestClassifyKinds(t *testing.T) {
	cases := []struct {
		stderr string
		err    error
		want   Kind
	}{
		{stderr: "RTNETLINK answers: Operation not permitted", err: errors.New("exit 2"), want: KindPermission},
		{stderr: "modprobe: FATAL: Module vcan not found", err: errors.New("exit 1"), want: KindMissingModule},
		{stderr: "Error: Unknown device type.", err: errors.New("exit 1"), want: KindMissingModule},
		{stderr: "", err: exec.ErrNotFound, want: KindMissingCommand},
		{stderr: "", err: context.DeadlineExceeded, want: KindTimeout},
		{stderr: "boom", err: errors.New("exit 1"), want: KindOther},
	}
	for _, c := range cases {
		got := classify("测试", "ip", Result{Stderr: c.stderr}, c.err)
		if got.Kind != c.want {
			t.Errorf("classify(%q) = %v，期望 %v", c.stderr, got.Kind, c.want)
		}
		if KindOf(got) != c.want {
			t.Errorf("KindOf 不一致：%v", KindOf(got))
		}
		if !strings.Contains(got.Error(), "测试") {
			t.Errorf("错误信息缺少操作名：%s", got.Error())
		}
	}
}

func TestAddVCANRollsBackOnUpFailure(t *testing.T) {
	fake := &FakeRunner{
		Handler: func(name string, args []string) (Result, error, bool) {
			if name == "ip" && len(args) > 2 && args[1] == "set" {
				return Result{Stderr: "RTNETLINK answers: Operation not permitted", Code: 2}, errors.New("exit 2"), true
			}
			return Result{}, nil, false
		},
	}
	h := &Host{Runner: fake}
	err := h.AddVCAN(context.Background(), "vcanbl1")
	if KindOf(err) != KindPermission {
		t.Fatalf("期望权限错误，得到 %v", err)
	}
	calls := fake.Calls()
	last := calls[len(calls)-1]
	if !strings.HasPrefix(last, "ip link delete") {
		t.Fatalf("失败后应回滚删除接口，实际调用序列：%v", calls)
	}
}

func TestAddVCANMissingIP(t *testing.T) {
	h := &Host{Runner: &FakeRunner{NotFound: map[string]bool{"ip": true}}}
	if got := KindOf(h.AddVCAN(context.Background(), "vcanbl1")); got != KindMissingCommand {
		t.Fatalf("期望命令缺失错误，得到 %v", got)
	}
}

func TestListLinksAndAllocate(t *testing.T) {
	h := &Host{Runner: &FakeRunner{Links: []string{"lo", "eth0", "vcanbl1"}}}
	links, err := h.ListLinks(context.Background())
	if err != nil {
		t.Fatalf("ListLinks 失败：%v", err)
	}
	if len(links) != 3 || links[2] != "vcanbl1" {
		t.Fatalf("解析结果异常：%v", links)
	}
	name, err := h.AllocateVCANName(context.Background())
	if err != nil || name != "vcanbl2" {
		t.Fatalf("AllocateVCANName = %q, %v", name, err)
	}
	orphans, err := h.OrphanVCANs(context.Background())
	if err != nil || len(orphans) != 1 || orphans[0] != "vcanbl1" {
		t.Fatalf("OrphanVCANs = %v, %v", orphans, err)
	}
}

func TestParseIPLinkJSON(t *testing.T) {
	out := `[{"ifindex":1,"ifname":"lo"},{"ifindex":7,"ifname":"vcanbl3"}]`
	names, err := parseIPLinkJSON(out)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(names) != 2 || names[1] != "vcanbl3" {
		t.Fatalf("names = %v", names)
	}
	if _, err := parseIPLinkJSON("not json"); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

func TestVCANIfaceNameLength(t *testing.T) {
	for _, i := range []int{1, 42, 999} {
		if n := vcanIfaceName(i); len(n) > 15 {
			t.Fatalf("接口名过长：%s", n)
		}
	}
}
