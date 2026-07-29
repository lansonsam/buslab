package model

import "testing"

func sample() *Project {
	p := NewProject("t")
	p.AddBus(&Bus{ID: "can1", Name: "CAN 1", Type: BusCAN, CAN: DefaultCANParams()})
	p.AddBus(&Bus{ID: "s232", Name: "232 1", Type: BusRS232, Serial: DefaultSerialParams()})
	p.AddBus(&Bus{ID: "s422", Name: "422 1", Type: BusRS422, Serial: DefaultSerialParams()})
	p.AddBus(&Bus{ID: "s485", Name: "485 1", Type: BusRS485, Serial: DefaultSerialParams()})
	for _, id := range []NodeID{"n1", "n2", "n3"} {
		p.AddNode(&Node{ID: id, Name: "节点" + string(id), Role: RoleNode})
	}
	return p
}

func TestValidateAttachSingleBus(t *testing.T) {
	p := sample()
	if err := ValidateAttach(p, "n1", "can1", RoleNode); err != nil {
		t.Fatalf("首次接入应合法：%v", err)
	}
	p.Node("n1").Bus = "can1"
	if err := ValidateAttach(p, "n1", "can1", RoleNode); err == nil {
		t.Fatal("重复接入同一总线应报错")
	}
	if err := ValidateAttach(p, "n1", "s485", RoleNode); err == nil {
		t.Fatal("已接入节点再接其它总线应报错")
	}
	if err := ValidateAttach(p, "missing", "can1", RoleNode); err == nil {
		t.Fatal("未知节点应报错")
	}
	if err := ValidateAttach(p, "n2", "missing", RoleNode); err == nil {
		t.Fatal("未知总线应报错")
	}
}

func TestValidateAttachRS232Limit(t *testing.T) {
	p := sample()
	p.Node("n1").Bus = "s232"
	p.Node("n2").Bus = "s232"
	if err := ValidateAttach(p, "n3", "s232", RoleNode); err == nil {
		t.Fatal("RS-232 第三个节点应被拒绝")
	}
}

func TestValidateAttachRS422SingleMaster(t *testing.T) {
	p := sample()
	p.Node("n1").Bus = "s422"
	p.Node("n1").Role = RoleMaster
	if err := ValidateAttach(p, "n2", "s422", RoleMaster); err == nil {
		t.Fatal("RS-422 第二个主机应被拒绝")
	}
	if err := ValidateAttach(p, "n2", "s422", RoleSlave); err != nil {
		t.Fatalf("从机接入应合法：%v", err)
	}
}

func TestCheckBusReady(t *testing.T) {
	p := sample()
	if err := CheckBusReady(p, "can1"); err == nil {
		t.Fatal("空总线应报错")
	}
	p.Node("n1").Bus = "can1"
	if err := CheckBusReady(p, "can1"); err != nil {
		t.Fatalf("单节点 CAN 应可启动：%v", err)
	}

	p.Node("n2").Bus = "s422"
	p.Node("n2").Role = RoleSlave
	if err := CheckBusReady(p, "s422"); err == nil {
		t.Fatal("RS-422 缺少主机应报错")
	}
	p.Node("n3").Bus = "s422"
	p.Node("n3").Role = RoleMaster
	if err := CheckBusReady(p, "s422"); err != nil {
		t.Fatalf("1 主 1 从应可启动：%v", err)
	}
}

func TestBusIssues(t *testing.T) {
	p := sample()
	issues := BusIssues(p)
	if len(issues) != 4 {
		t.Fatalf("四条空总线应各有一条提示，得到 %d 条：%v", len(issues), issues)
	}
	p.Node("n1").Bus = "can1"
	p.Node("n2").Bus = "can1"
	for _, is := range BusIssues(p) {
		if is.Bus == "can1" {
			t.Fatalf("双节点 CAN 不应有提示：%s", is.Message)
		}
	}
}

func TestValidateProject(t *testing.T) {
	p := sample()
	if err := ValidateProject(p); err != nil {
		t.Fatalf("样例项目应合法：%v", err)
	}
	p.Nodes[0].Bus = "ghost"
	if err := ValidateProject(p); err == nil {
		t.Fatal("引用不存在总线应报错")
	}
	p.Nodes[0].Bus = ""

	p.AddBus(&Bus{ID: "can1", Name: "重复", Type: BusCAN})
	if err := ValidateProject(p); err == nil {
		t.Fatal("重复总线 ID 应报错")
	}
}
