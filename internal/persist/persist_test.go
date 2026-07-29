package persist

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lansonsam/buslab/internal/model"
)

func demoProject() *model.Project {
	p := model.NewProject("演示")
	p.AddBus(&model.Bus{ID: "b1", Name: "CAN 1", Type: model.BusCAN, CAN: model.DefaultCANParams(),
		Pos: model.Point{X: 10, Y: 20}, Resource: "vcanbl1", Running: true})
	p.AddBus(&model.Bus{ID: "b2", Name: "485 1", Type: model.BusRS485, Serial: model.DefaultSerialParams()})
	p.AddNode(&model.Node{ID: "n1", Name: "ECU", Bus: "b1", Role: model.RoleNode,
		Pos: model.Point{X: 5, Y: 6}, Endpoint: "vcanbl1"})
	return p
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo"+FileExtension)
	in := demoProject()
	if err := Save(path, in); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("加载失败：%v", err)
	}
	if out.Name != in.Name || len(out.Buses) != 2 || len(out.Nodes) != 1 {
		t.Fatalf("加载结果不符：%+v", out)
	}
	if out.Buses[0].Pos.X != 10 || out.Nodes[0].Pos.Y != 6 {
		t.Fatal("画布坐标应被持久化")
	}
	if out.Buses[0].Resource != "" || out.Buses[0].Running || out.Nodes[0].Endpoint != "" {
		t.Fatal("运行期字段不应持久化")
	}
	if out.Buses[1].Serial != model.DefaultSerialParams() {
		t.Fatalf("串口参数丢失：%+v", out.Buses[1].Serial)
	}
}

func TestLoadRejectsBadData(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad"+FileExtension)
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Fatal("非法 JSON 应报错")
	}

	empty := filepath.Join(dir, "empty"+FileExtension)
	if err := os.WriteFile(empty, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(empty); err == nil {
		t.Fatal("缺少项目数据应报错")
	}

	future := filepath.Join(dir, "future"+FileExtension)
	if err := os.WriteFile(future, []byte(`{"version":99,"project":{"name":"x"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(future); err == nil {
		t.Fatal("高版本文件应报错")
	}

	if _, err := Load(filepath.Join(dir, "missing"+FileExtension)); err == nil {
		t.Fatal("文件缺失应报错")
	}
}

func TestSaveRejectsInvalidProject(t *testing.T) {
	p := model.NewProject("坏项目")
	p.AddNode(&model.Node{ID: "n1", Name: "孤儿", Bus: "ghost", Role: model.RoleNode})
	if err := Save(filepath.Join(t.TempDir(), "x"+FileExtension), p); err == nil {
		t.Fatal("引用不存在总线应拒绝保存")
	}
}

func TestEnsureExtension(t *testing.T) {
	cases := map[string]string{
		"a":                 "a" + FileExtension,
		"a.json":            "a" + FileExtension,
		"a" + FileExtension: "a" + FileExtension,
		"/tmp/x/y":          "/tmp/x/y" + FileExtension,
	}
	for in, want := range cases {
		if got := EnsureExtension(in); got != want {
			t.Errorf("EnsureExtension(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got := LoadSettings(); got != DefaultSettings() {
		t.Fatalf("缺省设置应为默认值：%+v", got)
	}
	s := Settings{LastProject: "/tmp/p" + FileExtension, LogLimit: 100, DarkTheme: false}
	if err := SaveSettings(s); err != nil {
		t.Fatalf("保存设置失败：%v", err)
	}
	if got := LoadSettings(); got != s {
		t.Fatalf("读回设置不符：%+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "buslab", "config.json")); err != nil {
		t.Fatalf("设置文件位置不符：%v", err)
	}
}
