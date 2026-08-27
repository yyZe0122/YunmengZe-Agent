# ADR 022：任务应用服务与 Core 查询边界

## 状态

Accepted，2026-07-16。交互 Planner / 人批路径已删除（见 [ADR-038](038-session-chat-boundary.md)）；本 ADR 仅保留仍有效的写入/查询边界。2026-08-13：`dependencies_test.go` 增补 TUI / CLI / Gateway 禁导入 effect 路径。2026-08-27：读模型跳过 `hidden_task_ids`。

## 背景

Gateway 与 Scheduler 都需要把外部请求转换为 Core Task。若每个适配器分别创建 Session/Task 并处理幂等，编排会分叉。Gateway 不得直接持有业务 `*sql.DB` 或了解 Core 表结构。

## 决策

### 命令应用层

`internal/tasksubmission.Service` 是“提交任务”用例的唯一编排入口：

1. 校验并规范化标题和目标；
2. 按请求生成或接受 Session、Task ID；
3. 确保 Session 存在且仍为 active；
4. 通过 Kernel Repository 创建 Task；
5. 对客户端指定 Task ID 做内容一致的幂等重试，拒绝冲突复用；
6. 按 `execution_mode` 委托 `chatsession`（agent 写 / plan 只读）。

该服务不复制 Kernel 状态机，不写 SQL。PlanDocument 与 workspace grant 由 `chatsession` 按 mode 构造（ADR-038）。

本地 Gateway `POST /v1/tasks` 与定时 Job（`scheduledtasks` → 本服务，ADR-042）共用此入口。

### 查询应用层

`internal/corequery.Store` 是 Core 本地读模型：只查询与 `PRAGMA quick_check`，公开 Task、Plan、Step、Approval、Run、TaskUsage、Plan Document 等窄 DTO。`sessions.metadata.hidden_task_ids` 从 transcript / ListTasks / GetTask / runs / session 计数中排除。列表分页/排序见 ADR-027；时间 UTC 见 ADR-029。

Gateway 只依赖自声明的 `QueryService` 接口，不持有 `*sql.DB`。

### 组合根

```text
SQLite -> Kernel Repository -> TaskSubmission -> ChatSession
SQLite -> CoreQuery Store    -> Gateway
TaskSubmission               -> Gateway POST /v1/tasks
TaskControl                  -> Gateway pause/resume/cancel
```

不引入容器 DI、ORM、通用 Repository 或事件溯源框架。

### 应用错误边界

用例结果经 `internal/applicationerror` 分类；Gateway 只映射该分类。见 ADR-026。

## 自动边界

`internal/architecture/dependencies_test.go`：

- `pkg/*` 不导入 `internal/*`；
- Gateway、GatewayClient、TaskSubmission、ScheduledTasks 不直接导入 `database/sql` 或 `store/sqlite`；
- `gatewayclient` 不导入 `gateway` server；
- TUI 与 `cmd/ymz` 不导入 `tools` / `agent` / `chatsession` / `store/sqlite` / `providerruntime` / `providers`；
- Gateway 不导入 `tools` / `agent` / `providerruntime` / `providers`。

## 后果

- 新传输入口复用 TaskSubmission，不复制领域编排；
- Core 表结构变化只需调整 Query Store 与领域仓储；
- 读模型与写模型分工明确，仍共享单个 `core.db`。
