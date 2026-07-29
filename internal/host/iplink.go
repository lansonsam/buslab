package host

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ResourcePrefix 是本程序创建的 vcan 接口前缀，清理时据此识别，避免误删用户接口。
const ResourcePrefix = "vcanbl"

// AddVCAN 创建并启用一条 vcan 接口。失败时尽力回滚半创建状态。
func (h *Host) AddVCAN(ctx context.Context, name string) error {
	if !h.runner().LookPath("ip") {
		return &Error{Op: "创建 " + name, Kind: KindMissingCommand, Detail: "ip"}
	}
	if _, err := h.run(ctx, "创建 "+name, "ip", "link", "add", "dev", name, "type", "vcan"); err != nil {
		return err
	}
	if _, err := h.run(ctx, "启用 "+name, "ip", "link", "set", "dev", name, "up"); err != nil {
		_ = h.DeleteLink(ctx, name)
		return err
	}
	return nil
}

func (h *Host) DeleteLink(ctx context.Context, name string) error {
	if !h.runner().LookPath("ip") {
		return &Error{Op: "删除 " + name, Kind: KindMissingCommand, Detail: "ip"}
	}
	_, err := h.run(ctx, "删除 "+name, "ip", "link", "delete", "dev", name)
	return err
}

// ListLinks 返回全部网络接口名，优先使用 ip -j 的 JSON 输出。
func (h *Host) ListLinks(ctx context.Context) ([]string, error) {
	if !h.runner().LookPath("ip") {
		return nil, &Error{Op: "枚举接口", Kind: KindMissingCommand, Detail: "ip"}
	}
	res, err := h.run(ctx, "枚举接口", "ip", "-j", "link", "show")
	if err == nil {
		if names, jerr := parseIPLinkJSON(res.Stdout); jerr == nil {
			return names, nil
		}
	}
	res, err = h.run(ctx, "枚举接口", "ip", "link", "show")
	if err != nil {
		return nil, err
	}
	return parseIPLinkText(res.Stdout), nil
}

func parseIPLinkJSON(out string) ([]string, error) {
	var links []struct {
		IfName string `json:"ifname"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &links); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(links))
	for _, l := range links {
		if l.IfName != "" {
			names = append(names, l.IfName)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("ip -j link show 未返回接口")
	}
	return names, nil
}

var ipLinkLine = regexp.MustCompile(`^\d+:\s+([^:@]+)`)

func parseIPLinkText(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		m := ipLinkLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m != nil {
			names = append(names, strings.TrimSpace(m[1]))
		}
	}
	return names
}

// AllocateVCANName 找出一个未被占用的 vcanbl 名称。
func (h *Host) AllocateVCANName(ctx context.Context) (string, error) {
	existing, err := h.ListLinks(ctx)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(existing))
	for _, n := range existing {
		used[n] = true
	}
	for i := 1; i < 1000; i++ {
		name := vcanIfaceName(i)
		if !used[name] {
			return name, nil
		}
	}
	return "", fmt.Errorf("没有可用的 %s* 接口名", ResourcePrefix)
}

// OrphanVCANs 列出上次异常退出遗留的本程序接口。
func (h *Host) OrphanVCANs(ctx context.Context) ([]string, error) {
	links, err := h.ListLinks(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range links {
		if strings.HasPrefix(n, ResourcePrefix) {
			out = append(out, n)
		}
	}
	return out, nil
}
