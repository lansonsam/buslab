# 虚拟总线实验室 实现计划（Go + Fyne）

- **日期**：2026-07-29
- **对应设计**：`docs/superpowers/specs/2026-07-29-virtual-bus-lab-design.md`
- **模块名**：`github.com/lansonsam/buslab`
- **Go**：1.26.4（开发机 windows/amd64，目标 linux/arm64）
- **Fyne**：v2.8.0

## 0. 开发环境事实（影响验证方式）

| 事实 | 影响 |
|------|------|
| 开发机无 C 编译器，`CGO_ENABLED=0` | `fyne.io/fyne/v2/app`（glfw/GL）在本机不可编译 |
| `fyne.io/fyne/v2/test` 驱动纯 Go 可用 | UI 组件可在本机无 CGO 编译并做单元测试 |
| 目标为 linux/arm64 | 内核相关代码用 `//go:build linux`，其余平台提供 unsupported stub |

**约束（编码纪律）**：只有 `cmd/buslab/main.go` 允许 import `fyne.io/fyne/v2/app`；
`internal/ui` 只用 `widget`/`container`/`canvas`/`theme`/`driver` 等纯 Go 包，
使得 `go build ./internal/...` 与 `go test ./internal/...` 在开发机可跑。

## 1. 目录与包边界

```text
cmd/buslab/main.go            # app.New + ui.New + 退出清理（仅目标机构建）
internal/model/               # 纯数据 + 拓扑校验（无外部依赖）
internal/host/                # 命令执行、权限/模块探测、孤儿资源清理
internal/adapt/               # Adapter 公共接口（Bus/Endpoint/Provider）
internal/adapt/canbus/        # vcan + SocketCAN（linux）/ stub（其它）
internal/adapt/serialbus/     # tty0tty/pty 探测、pair、SerialHub
internal/orch/                # Orchestrator：命令、事件、生命周期
internal/ui/                  # 布局、画布、属性、发送、日志、状态栏
internal/persist/             # *.buslab.json 读写
```

依赖方向：`ui → orch → adapt → host`，`model` 被所有层引用且不依赖任何内部包。

## 2. 关键接口（先定接口，后填实现）

```go
// internal/adapt
type Frame = model.Frame

type Endpoint interface {
    Name() string            // vcan-buslab-1 / /dev/tnt0
    Send(model.Frame) error
    Close() error
}

type Bus interface {
    Kind() model.BusType
    Resource() string        // 内核资源名，UI 展示
    Open(node model.NodeID, role model.NodeRole) (Endpoint, error)
    Close() error            // 释放全部 endpoint + 内核资源
}

type Provider interface {
    Kind() model.BusType
    Probe() model.HostReport  // 权限/模块/设备可用性
    Create(spec model.BusSpec) (Bus, error)
}
```

- 收到的帧通过 `Provider` 创建时传入的 `func(model.Frame)` 回调上抛（Adapter 内部读 goroutine）。
- `internal/adapt/fake`（memory provider）用于 orch/ui 测试，无内核依赖。

## 3. 阶段化实现（每阶段结束都要 build + vet + test 通过）

### 阶段 A：骨架与领域模型（本机可验证）
1. `internal/model`：`BusType`/`NodeRole`/`Frame`/`Bus`/`Node`/`Project`，
   `Frame.Summary()`、CAN 帧字段校验、hex 解析工具。
2. `internal/model/validate.go`：设计 §4.3 全部规则 + 单测（表驱动）。
3. `internal/persist`：Project JSON 存取 + round-trip 单测。

### 阶段 B：Host 与 Adapter 抽象
4. `internal/host`：`Runner`（exec + 超时 + stderr 捕获）、`Error` 分类
   （权限不足 / 模块缺失 / 命令缺失 / 其它），`Detect()` 返回 `model.HostReport`。
   linux 用 `/proc/self/status` 的 `CapEff` 判定 `CAP_NET_ADMIN`；其它平台返回 unsupported。
5. `internal/adapt`：上节接口 + `fake` provider（内存总线，广播语义可配）+ 单测。

### 阶段 C：CAN Adapter（linux 实现 / 本机交叉 vet）
6. `EnsureModule`（`modprobe vcan`）、`CreateBus`（`ip link add <name> type vcan` + `up`）、
   `DeleteBus`（`ip link delete`），命名 `vcanbl<N>`（≤15 字符 IFNAMSIZ 限制）。
7. SocketCAN 收发：`golang.org/x/sys/unix` 原生 `AF_CAN/SOCK_RAW/CAN_RAW`（无 CGO），
   `can_frame` 16 字节编解码（`internal/adapt/canbus/frame.go`，纯 Go，本机单测）。
8. 非 linux：`bus_stub.go` 返回 `ErrUnsupported`，保证 `go build` 全平台通过。

### 阶段 D：Serial Adapter + Hub
9. `Hub`（纯 Go，`io.ReadWriteCloser` 抽象）：
   - `rs232`：两端直通
   - `rs485`：任一端 TX → 其余端 RX；并发发送记 `Collision` 事件
   - `rs422`：Master→所有 Slave，Slave→仅 Master
   用 `net.Pipe` 写单测（本机可跑）。
10. linux 端口提供者：优先 `tty0tty`（扫描 `/dev/tnt*` 成对占用），回退 `/dev/ptmx` openpty
    （`TIOCSPTLCK`/`TIOCGPTN`，纯 Go），termios 设置波特率/数据位/校验/停止位。
11. 回退 pty 时置 `report.Warning = "仅本进程可见"`，UI 显示。

### 阶段 E：Orchestrator
12. 命令方法（`CreateBus`/`DeleteBus`/`AddNode`/`AttachNode`/`DetachNode`/
    `StartMonitor`/`StopMonitor`/`SendFrame`/`Save`/`Load`）+ 事件广播（订阅者 channel，
    非阻塞丢弃 + 计数），全部走互斥锁保护的状态机。
13. 创建失败要回滚（不留半创建 Bus）；`StopAll()` 供退出钩子调用。
14. 用 fake provider 写 orch 单测：附着校验、发送扇出、事件顺序、清理幂等。

### 阶段 F：UI（Fyne）
15. `internal/ui/app.go`：`New(orch *orch.Orchestrator) fyne.CanvasObject`，
    `HSplit(左画布, 右侧 VSplit(属性/发送/日志))` + 顶部工具栏 + 底部状态栏。
16. 画布：自绘 `widget.BaseWidget`，总线画为横向母线（按类型着色：CAN 蓝 / 485 橙 /
    422 绿 / 232 灰），节点为可拖拽方块，拖到母线附近即 Attach；点击选中同步右侧属性。
17. 发送面板：CAN（ID/扩展帧/DLC/Data hex）与串口（ASCII/Hex）两套，随选中总线切换。
18. 日志：`widget.Table` + 环形缓冲（上限可配，默认 5000），暂停、按总线过滤。
19. 状态栏：权限 / 模块 / tty 后端 / 活动资源数；异常用 `Error` 事件弹对话框。
20. UI 单测：`fyne.io/fyne/v2/test` + fake provider，覆盖「创建总线→添加节点→发送→日志出现」。

### 阶段 G：入口与收尾
21. `cmd/buslab/main.go`：`app.NewWithID("ai.factory.buslab")`，窗口 1280x800，
    `SetCloseIntercept` → `orch.StopAll()`；启动时孤儿扫描（`ip -j link show` 里 `vcanbl*`）。
22. `Makefile` / `scripts/build-arm64.sh`（目标机构建命令与依赖说明）。
23. `docs/` 补一份运行前置说明（modprobe vcan、tty0tty、权限）——仅在实现完成后写。

## 4. 验证矩阵

| 命令 | 位置 | 目的 |
|------|------|------|
| `go build ./internal/... ` | 开发机（CGO=0） | 全部内部包可编译 |
| `go test ./internal/...` | 开发机 | model/hub/frame/orch/ui 单测 |
| `GOOS=linux GOARCH=arm64 go vet ./internal/...` | 开发机 | linux 专属文件类型检查 |
| `go build ./...` | ARM Linux | 含 Fyne app 的完整构建 |
| 手工 | ARM Linux | 设计 §14 四条成功标准 |

`root` 依赖的集成测试打 `//go:build linux && integration` 标签，默认跳过。

## 5. 顺序与里程碑

- **M1（P0 演示）**：阶段 A–C + F 的最小画布/日志 + G → CAN 双节点互发、RS-232 直通。
- **M2**：阶段 D 完整（485/422 多节点）+ 项目保存加载 + 日志过滤。
- **M3**：外部工具互通（`candump`/`minicom`）文档与错误注入。

## 6. 风险与应对（实现层）

| 风险 | 应对 |
|------|------|
| 本机无法编译 fyne app 包 | UI 逻辑下沉到 `internal/ui`，`main` 保持 <60 行；目标机最终构建 |
| pty 无法被外部工具枚举 | 在状态栏与属性面板明确标注，优先 tty0tty |
| `ip` 输出解析脆弱 | 用 `ip -j`（JSON）解析，失败再退回文本 |
| Fyne 主线程约束 | 所有事件回调经 `fyne.Do` 派发，Adapter 读循环独立 goroutine |
| vcan 名超长 | 名称生成限制 ≤15 字符并做单测 |
