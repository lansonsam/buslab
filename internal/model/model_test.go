package model

import (
	"reflect"
	"testing"
)

func TestParseHexBytes(t *testing.T) {
	cases := []struct {
		in   string
		want []byte
		err  bool
	}{
		{in: "11 22 33", want: []byte{0x11, 0x22, 0x33}},
		{in: "0x11,0x22", want: []byte{0x11, 0x22}},
		{in: "1122AA", want: []byte{0x11, 0x22, 0xAA}},
		{in: "11-22:33_44", want: []byte{0x11, 0x22, 0x33, 0x44}},
		{in: "1 2 3", want: []byte{0x01, 0x02, 0x03}},
		{in: "112", err: true},
		{in: "zz", err: true},
		{in: "   ", err: true},
	}
	for _, c := range cases {
		got, err := ParseHexBytes(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseHexBytes(%q) 期望报错，得到 %X", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseHexBytes(%q) 意外报错：%v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseHexBytes(%q) = %X，期望 %X", c.in, got, c.want)
		}
	}
}

func TestParseCANID(t *testing.T) {
	if v, err := ParseCANID("0x123"); err != nil || v != 0x123 {
		t.Fatalf("ParseCANID(0x123) = %X, %v", v, err)
	}
	if v, err := ParseCANID(" 7ff "); err != nil || v != 0x7FF {
		t.Fatalf("ParseCANID(7ff) = %X, %v", v, err)
	}
	if _, err := ParseCANID("xyz"); err == nil {
		t.Fatal("ParseCANID(xyz) 期望报错")
	}
}

func TestFrameValidate(t *testing.T) {
	if err := (Frame{Kind: BusCAN, CANID: 0x800}).Validate(); err == nil {
		t.Fatal("标准帧 ID 越界应报错")
	}
	if err := (Frame{Kind: BusCAN, CANID: 0x800, Ext: true}).Validate(); err != nil {
		t.Fatalf("扩展帧 0x800 应合法：%v", err)
	}
	if err := (Frame{Kind: BusCAN, Data: make([]byte, 9)}).Validate(); err == nil {
		t.Fatal("超过 8 字节应报错")
	}
	if err := (Frame{Kind: BusRS232}).Validate(); err == nil {
		t.Fatal("串口空数据应报错")
	}
}

func TestFrameSummary(t *testing.T) {
	f := Frame{Kind: BusCAN, CANID: 0x123, Data: []byte{1, 2}}
	if got, want := f.Summary(), "ID=0x123 [2] 01 02"; got != want {
		t.Errorf("Summary = %q，期望 %q", got, want)
	}
	s := Frame{Kind: BusRS232, Data: []byte("Hi")}
	if got, want := s.Summary(), "48 69  |Hi|"; got != want {
		t.Errorf("Summary = %q，期望 %q", got, want)
	}
	bin := Frame{Kind: BusRS485, Data: []byte{0x00, 0x01}}
	if got, want := bin.Summary(), "00 01"; got != want {
		t.Errorf("Summary = %q，期望 %q", got, want)
	}
}

func TestSerialParamsValidate(t *testing.T) {
	p := DefaultSerialParams()
	if err := p.Validate(); err != nil {
		t.Fatalf("默认参数应合法：%v", err)
	}
	if p.String() != "115200 8N1" {
		t.Fatalf("String = %q", p.String())
	}
	bad := p
	bad.DataBits = 9
	if err := bad.Validate(); err == nil {
		t.Fatal("数据位 9 应报错")
	}
}

func TestProjectNextNameAndRemove(t *testing.T) {
	p := NewProject("")
	if p.Name == "" {
		t.Fatal("空名称应有默认值")
	}
	bus := &Bus{ID: "b1", Name: p.NextName("CAN 总线"), Type: BusCAN}
	p.AddBus(bus)
	if bus.Name != "CAN 总线 1" {
		t.Fatalf("NextName = %q", bus.Name)
	}
	if next := p.NextName("CAN 总线"); next != "CAN 总线 2" {
		t.Fatalf("NextName = %q", next)
	}
	node := &Node{ID: "n1", Name: "节点 1", Bus: "b1", Role: RoleNode}
	p.AddNode(node)
	p.RemoveBus("b1")
	if len(p.Buses) != 0 {
		t.Fatal("总线未删除")
	}
	if node.Attached() {
		t.Fatal("删除总线后节点应变为未连接")
	}
	p.RemoveNode("n1")
	if len(p.Nodes) != 0 {
		t.Fatal("节点未删除")
	}
}
