# ADR-001：核心边界

- 状态：Accepted
- 日期：2026-07-13
- 更新：2026-08-24（ADR-053：TUI Charm v2；零进程边界变化）

## 决策

YunmengZe 的生产核心是一个 `ymzd` 进程、一个 `ymz` CLI（含 TUI）和一个 `core.db`。Core 直接组合 Kernel、Chat Session（agent/plan 双轨）、Agent Runner、Policy、Approval/Grant 领域、Tool Broker、Scheduler（in-process store + chat-native `scheduledtasks` runner）、Event、Audit、Skill Catalog、MCP（经 Broker）、Gateway 以及窄应用服务（`tasksubmission` / `chatsession` / `taskcontrol` / `corequery`）。双轨见 ADR-038；定时 Job 见 ADR-042。

Task、Plan、Run 和 Job 是持久化领域对象，不是操作系统进程。需要并发时优先使用受 `context.Context` 控制的 goroutine；只有实际 Tool 执行需要时才启动外部进程。

通用 Module Runtime、Supervisor、私有模块 RPC、Capability Registry 聚合和进程外 Memory/Scheduler/Evolution 已从生产路径删除。交互 Planner 审批轨与 plan-step Start 已删除。未来子代理应建模为带 `parent_run_id` 的逻辑子 Run，共享现有预算、授权和审计边界，而不是恢复通用模块框架（详见 ADR-039；触发工具名为 `task`，同步阻塞）。

Gateway 不直接拼装领域状态：任务提交与 chat 运行进入窄应用服务。所有模型请求的可执行操作必须经过 Tool Broker；Skill、Provider、Scheduler 和 Gateway 都不能绕过 Policy、Grant 与 Audit。

本地 CLI 子命令与 TUI 是 Gateway 的并列客户端（`internal/gatewayclient`），不打开 `core.db`、不执行 Tool、不调用 Provider。详见 ADR-018、ADR-037。

## Go 实现原则

- 组合根集中在 `cmd/ymzd`，使用具体类型和小接口；接线按阶段放在同包 `wire_{store,tools,chat,gateway}.go` 与 `adapters.go`，`main.go` 只保留 `run` / flag / defer / `--check`；
- 状态机和不变量留在领域包与仓储中（`kernel` 仓储可按聚合同包再拆，不抽通用 Repository 包）；
- 大文件只同包切开（`tui/cmds_*`、`tui/update_*`、`tui/view_*`、`tui/{raster,paths,mascot,panel}.go`、`tools/fs_*`）；新 slash / 新聚合 SQL / 接线难 review 时再拆，不新建包；TUI 引擎见 ADR-053；
- 不引入 ORM、通用 Repository、容器式 DI 或事件总线框架；
- 新抽象必须由当前用户故事证明，而不是为假设中的扩展预留。
