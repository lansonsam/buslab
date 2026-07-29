# 虚拟总线实验室（Go + Fyne）设计方案

- **日期**：2026-07-29
- **状态**：草案（需求已对齐；待实现计划）
- **工作区**：`C:\Users\lansonsam\Desktop\CAN`

## 1. 目标与非目标

### 1.1 目标

在 **ARM Linux 桌面** 上提供图形化「虚拟总线实验室」：

- 本机模拟 **CAN / RS-232 / RS-422 / RS-485**
- **多节点总线拓扑**（非仅点对点）
- 基于 **内核能力**（`vcan` / `pty` / `tty0tty`），GUI 负责配置、连线、收发与监视
- 界面：**左拓扑画布 + 右收发/日志/属性**

### 1.2 非目标（MVP 不做）

- Windows / macOS 一等公民支持（可后续加抽象，首版只保证 ARM Linux）
- 真实硬件深度调试器（DBC/LDF 解析、示波器级时序、错误注入全套）
- 浏览器远程 UI / 无头守护进程分离部署
- 商业级拓扑编辑器（Bezier、对齐吸附、撤销栈等可后置）

## 2. 已确认决策

| 项 | 选择 |
|----|------|
| 产品形态 | A：虚拟总线实验室 |
| 协议范围 | CAN + 232 + 422 + 485，首版即多节点拓扑 |
| 运行环境 | B：ARM Linux + 本机桌面 GUI |
| 虚拟实现 | B：内核增强（`vcan` / `pty` / `tty0tty`） |
| UI 形态 | C：左拓扑 + 右收发/日志 |
| 技术路线 | 方案 1：Fyne 单进程纯 Go |

## 3. 总体架构

```text
┌─────────────────────────────────────────┐
│  UI（Fyne）                              │
│  左：拓扑画布 │ 右：收发 / 日志 / 属性    │
└─────────────────┬───────────────────────┘
                  │ 命令 / 事件
┌─────────────────▼───────────────────────┐
│  Orchestrator（编排层）                   │
│  Bus / Node / Endpoint 生命周期           │
│  拓扑校验 · 会话状态 · 流量扇出           │
└─────────────────┬───────────────────────┘
                  │
     ┌────────────┼────────────┐
     ▼            ▼            ▼
┌─────────┐ ┌──────────┐ ┌────────────┐
│ CAN     │ │ Serial   │ │ HostExec   │
│ Adapter │ │ Adapter  │ │ (ip/modprobe)│
│ SocketCAN│ │ pty/tty0tty│ │            │
└─────────┘ └──────────┘ └────────────┘
     │            │
     ▼            ▼
  vcanX      /dev/tnt* 或 pty 对
```

### 3.1 不变式

1. GUI 不直接操作内核；只通过 Orchestrator 发命令、订阅事件。
2. 每种物理语义对应一类 **Bus**（`can` / `rs232` / `rs422` / `rs485`），节点挂在 Bus 上。
3. 内核资源由 Adapter 创建/销毁；进程退出时尽量清理（best-effort + 启动时孤儿检测）。
4. 流量统一为 `Frame` 事件，进入监视面板与日志。

### 3.2 进程模型

- 单二进制桌面应用。
- MVP 不做 `--headless`；接口预留便于后续拆守护进程。

## 4. 领域模型

### 4.1 实体

| 实体 | 含义 |
|------|------|
| `Project` | 一次实验会话：名称、拓扑、运行状态 |
| `Bus` | 一条虚拟总线：类型、内核资源句柄、参数（比特率等） |
| `Node` | 逻辑节点（ECU / 主机 / 从机），挂在某条 Bus 上 |
| `Endpoint` | 节点在总线上的接入点（对应 `vcan` 套接字或某个 `tty`） |
| `Link` | 画布上的视觉连线；语义上表示「节点属于该 Bus」 |
| `Frame` | 统一流量事件：时间戳、总线、方向、原始字节/CAN ID+DLC+Data |

### 4.2 总线语义（内核映射）

| 类型 | 拓扑语义 | 内核映射（MVP） |
|------|----------|-----------------|
| `can` | 多节点共享介质 | 一条 `vcanN`；每节点一个 SocketCAN 套接字加入同接口 |
| `rs485` | 半双工多点 | 以 `tty0tty` 星型或用户态转发枢纽：中心 `hub` 进程/ goroutine 在成对 tty 间广播（见下） |
| `rs422` | 全双工点对多（简化） | 主节点一对多：主侧 fan-out，从侧一对一回主（pty/`tty0tty`） |
| `rs232` | 点对点 | 一对 `tty0tty`（或 `pty` 对）；画布上限制两端 |

**说明（485/422 现实约束）**：Linux 没有与 `vcan` 对等的「内核虚拟 485 总线」。在「内核增强」前提下：

- **优先**：用 `tty0tty` / `pty` 提供真实 `tty` 设备节点，便于外部工具（`minicom`、自研程序）打开同一设备。
- **多点广播**：由本进程内 **Serial Hub** 完成（打开各端 tty，按 485 半双工规则转发）；不是纯内核广播。
- 文档与 UI 需标明：「虚拟 485/422 多点 = tty 设备 + 应用层 hub」，避免用户以为存在 `v485` 内核类型。

### 4.3 拓扑校验规则

- 同一 `Node` 在同一时刻只能属于一条同类型 Bus（MVP）；跨总线网关后期再做。
- `rs232`：Bus 上最多 2 个 Endpoint。
- `rs485` / `can`：≥1 个 Endpoint；建议 UI 引导 ≥2。
- `rs422`：恰好 1 个 Master + N 个 Slave（N≥1）。
- 删除 Bus 前必须停止收发并释放内核资源。

## 5. 模块与目录建议

```text
cmd/buslab/          # main：Fyne App 入口
internal/ui/         # 画布、收发面板、日志、对话框
internal/orch/       # Orchestrator：命令、状态机、事件总线
internal/model/      # Project/Bus/Node/Frame 纯数据结构
internal/adapt/can/  # vcan 创建、SocketCAN 收发
internal/adapt/serial/ # tty0tty/pty + Serial Hub
internal/host/       # modprobe、ip link、权限检测、清理
internal/persist/    # 拓扑 JSON 读写（可选 MVP）
```

依赖方向：`ui → orch → adapt/host`；`model` 无向外依赖。

## 6. Adapter 设计

### 6.1 CAN Adapter

**职责**

- `EnsureModule()`：`can` / `vcan` 已加载
- `CreateBus(name) → vcanX`：`ip link add … type vcan` + `up`
- `OpenEndpoint(ifname) → Conn`：`socket(AF_CAN, SOCK_RAW, CAN_RAW)` + bind
- `DeleteBus(ifname)`：`ip link delete`
- 收发：标准 CAN 帧；MVP 可选扩展帧；不做 FD 除非显式加开关

**权限**：需要 `CAP_NET_ADMIN` 或 root；启动时检测并给出明确 UI 提示（polkit/sudo 说明）。

### 6.2 Serial Adapter + Hub

**职责**

- 探测 `tty0tty`（`/dev/tnt*`）；若无则回退 `pty` 对（仅本进程可用，外部工具不可见——UI 警告）
- `CreatePair() → (A, B)`
- `SerialHub`：按总线类型转发
  - `rs232`：A↔B 直通
  - `rs485`：任意端 TX → 其余端 RX（模拟总线）；可选「同时仅一端发送」冲突检测日志
  - `rs422`：Slave→Master，Master→所有 Slave

**参数（MVP）**：波特率、数据位、校验、停止位；485 方向脚用软件逻辑模拟，不暴露真实 GPIO。

### 6.3 HostExec

封装 `modprobe`、`ip`；统一超时、stderr 捕获、错误码映射为 `host.Error`（含「权限不足」「模块不存在」）。

## 7. UI 设计（Fyne）

### 7.1 主布局

```text
┌──────────────┬────────────────────────────┐
│ 工具栏        │ 工具栏（发送/清空/过滤器）   │
├──────────────┼────────────────────────────┤
│              │ 选中对象属性                 │
│  拓扑画布     ├────────────────────────────┤
│  (Bus/Node)  │ 发送面板（按总线类型切换）   │
│              ├────────────────────────────┤
│              │ 流量日志（表格/虚拟列表）    │
└──────────────┴────────────────────────────┘
│ 状态栏：权限 / 内核模块 / 资源占用           │
└─────────────────────────────────────────────┘
```

### 7.2 交互（MVP）

- 工具栏：新建 CAN 总线、新建 485/422/232 总线、添加节点、删除、启动/停止监视
- 画布：拖拽节点；将节点「吸附」到总线（创建 Link）
- 选中总线/节点 → 右侧属性可编辑名称与串口参数
- 发送面板：
  - CAN：ID、DLC、Data hex、扩展帧勾选
  - 串口：ASCII / Hex 切换、发送按钮、可选周期发送（后期）
- 日志：时间、总线、节点、方向、摘要；支持暂停与按总线过滤

### 7.3 视觉原则（克制）

- 深色工控风可选用，但避免紫霓虹/过度发光
- 总线用颜色区分类型（如 CAN 蓝、485 橙、422 绿、232 灰）
- 活动流量时节点边框短暂高亮（轻量动画 1～2 处即可）

## 8. 数据流

### 8.1 命令（UI → Orch）

`CreateBus` / `DeleteBus` / `AddNode` / `AttachNode` / `DetachNode` / `StartMonitor` / `StopMonitor` / `SendFrame` / `SaveProject` / `LoadProject`

### 8.2 事件（Orch → UI）

`BusCreated` / `BusDeleted` / `NodeChanged` / `FrameRx` / `FrameTx` / `Error` / `HostStatus`

事件经 Fyne 主线程派发（`fyne.Do` / 渠道收敛），Adapter 的读循环在独立 goroutine。

### 8.3 发送路径

1. UI `SendFrame`
2. Orch 校验节点已 Attach 且监视中
3. 对应 Adapter `Write`
4. 发出 `FrameTx`；经 Hub/总线后其他 Endpoint 读到 → `FrameRx`

## 9. 错误处理与清理

| 场景 | 行为 |
|------|------|
| 无 `CAP_NET_ADMIN` | 状态栏红字 + 对话框；CAN 功能禁用，串口仍可用（若有 tty） |
| 无 `vcan` 模块 | 提示 `modprobe vcan`；提供「一键尝试」按钮 |
| 无 `tty0tty` | 回退 pty，并标明「仅本进程可见」 |
| `ip link` 失败 | 错误入日志，事务回滚（不留下半创建 Bus） |
| 应用退出 | `StopAll` → 关套接字 → `ip link delete` 本会话创建的 vcan → 释放 tty |
| 异常崩溃 | 启动时扫描「buslab-」前缀命名资源并可选清理 |

资源命名约定：`vcan-buslab-<id>` 或 `buslab<N>`，避免误删用户自有接口。

## 10. 配置与持久化

- 项目文件：`*.buslab.json`（总线类型、节点、画布坐标、串口参数；**不含**内核临时名的强绑定，打开时重新 Create）
- 应用设置：`$XDG_CONFIG_HOME/buslab/config.json`（上次路径、日志上限、主题）

## 11. 测试策略

| 层级 | 内容 |
|------|------|
| 单元 | 拓扑校验、485/422 Hub 转发规则、Frame 编解码 |
| 集成（Linux） | 真实 `vcan` 创建/收发（CI 用 privileged 或跳过标签 `root`） |
| 手工（ARM 板） | Fyne 窗口、拖拽、权限不足路径、退出清理 |

无内核环境时：为 Adapter 提供 `Interface` + memory fake，保证 Orch/UI 可测。

## 12. 分阶段交付

### P0（可演示 MVP）

- Fyne 主窗 + 左画布简版（方块节点 + 直线）
- CAN：`vcan` 创建、双节点互发、日志
- RS-232：一对虚拟 tty、双端收发
- 权限/模块检测与退出清理

### P1

- RS-485 / RS-422 Hub 多节点
- 项目保存/加载
- 日志过滤、发送面板完善

### P2

- 与外部进程互通验收文档（`candump` / `minicom`）
- 可选挂载真实 `can0` / 真实串口（只读监视或桥接开关）
- 轻度错误注入（485 冲突提示、CAN 总线关闭模拟）

## 13. 风险与缓解

| 风险 | 缓解 |
|------|------|
| ARM 板无桌面/GPU 弱 | 文档要求 Wayland/X11；Fyne 软件渲染验证 |
| `tty0tty` 未预装 | 打包说明 + pty 回退 |
| 用户以为存在内核 `v485` | UI 文案与本设计 §4.2 一致 |
| 权限导致 CAN 不可用 | 启动检测；提供串口子集仍可用 |

## 14. 成功标准（MVP）

1. 在目标 ARM Linux 桌面启动窗口，创建一条 `vcan`，两节点互发可见日志。
2. 创建一对虚拟 RS-232，两节点互发成功；若有 `tty0tty`，外部 `minicom` 可打开一端。
3. 退出后本会话创建的 `vcan` 被删除，无残留（或启动时可一键清理）。
4. 无管理员权限时有明确提示，不静默失败。

## 15. 下一步

1. 用户审阅本文件，确认或修订。
2. 通过后编写实现计划（`docs/superpowers/plans/…`）并开始编码。
