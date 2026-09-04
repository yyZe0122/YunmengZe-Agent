# 030 Agent Run 执行记录与恢复边界

## 状态

已接受，2026-07-17。更新：2026-09-03（`ResumePrompt` 可在最终 assistant / 半截历史后继续，ADR-039）。

## 背景

YunmengZe 已经有 Session、Task、Plan、Run、Approval、Capability Grant、Tool Call、Audit 和不可变 Event，但最小 Agent Loop 原先只在内存中维护 Provider Message。进程在以下位置退出时，调用方无法可靠判断应该继续、返回已有结果，还是重新执行：

- Provider 已返回 Tool Call，但 Tool Broker 尚未开始；
- Tool 已成功且 Grant 已消费，但 Tool Result 尚未加入下一轮 Provider 请求；
- Provider 已返回最终回答，但调用方尚未收到结果。

恢复不能以“再次调用一次”为默认策略。高风险工具可能已经产生外部效果，Grant 也可能已经消费；自动重试会造成重复执行或重复授权消费。

## 决策

### 唯一事实源

| 信息 | 唯一事实源 | 说明 |
|---|---|---|
| Session、Task、Plan、Run 状态 | Core 领域表与对应不可变 Event | Agent 不复制聚合状态 |
| Approval 决定 | `approvals` 与 Approval Event | 决定绑定 Plan Hash/Revision/Scope |
| Capability Grant 及消费次数 | `capability_grants` | Agent 不缓存剩余次数 |
| Agent 对话顺序 | `agent_run_records` | 每个 Run 按 `position` 追加，禁止更新和删除 |
| Tool 执行状态与响应 | `tool_calls` | `agent_run_records` 中的 Tool Result 只用于 Provider 对话重放 |
| Tool 安全审计 | `audit_log` | 保留 started/denied/succeeded/failed/timed_out/cancelled 结果 |

`agent_run_records` 保存 Provider-neutral Message、Provider Usage 和 Finish Reason，不保存厂商私有响应。记录类型只有初始消息、Assistant 消息和 Tool Result，避免同时维护第二套 Session 或 Tool 状态模型。

### 顺序与持久化点

1. Run 首次执行时，在调用 Provider 前原子写入全部初始 Message；恢复时必须提交完全相同的初始前缀。
2. Provider 返回 Tool Call 后，先追加 Assistant Message，再进入 Tool Broker。
3. Tool Broker 完成并把成功结果写入 `tool_calls` 后，Agent 才追加 Tool Result Message。
4. Provider 返回最终回答后，先追加最终 Assistant Message，再向调用方返回。
5. Approval 与 Grant 继续在 Agent Run 之外由 Core 创建。当前 Agent 只执行已经选定且已具备所需 Grant 的 Plan Step；交互式 Approval 等待属于后续权限交互阶段。

### 恢复算法

恢复按 `agent_run_records.position` 重放，并执行以下 fail-closed 检查：

- 初始 Message 与持久化前缀不一致：返回 `ErrRecoveryConflict`；
- Tool Result 没有对应的前序 Tool Call，或顺序不一致：返回 `ErrCorruptHistory`；
- 已持久化 Tool Result 与 `tool_calls` 中的成功响应不一致：返回 `ErrCorruptHistory`；
- Assistant Tool Call 缺少 Tool Result，但 `tool_calls` 已有 `succeeded` 响应：从已有响应补写 Tool Result，然后继续 Provider；
- Assistant Tool Call 缺少 Tool Result，且 `tool_calls` 不存在或不是 `succeeded`：返回 `ErrRecoveryBlocked`，不调用 Provider、不执行 Tool、不再次消费 Grant；
- 已有最终 Assistant Message：直接返回持久化结果，不再次调用 Provider。例外：`RunRequest.ResumePrompt`（ADR-039 `task_id` 续跑）在 restore 之后追加 user 再继续，即使上次已是最终 assistant 或历史半截。

因此，恢复只会补齐“已确认成功执行但消息尚未追加”的安全缺口，不会自动重试状态不明、失败、超时、取消或拒绝的工具调用。调用方需要创建新 Run、重新审批或重新规划。

### Session 裁剪、压缩和广播

当前阶段不实现自动上下文裁剪或摘要压缩。没有真实 Token 压力和摘要恢复用例前，不复制 Crush 的 Summary Message/Session Usage 模型。未来增加压缩时，摘要必须作为新的追加记录或明确的派生快照，不能修改既有执行记录。

领域事件继续使用 Core `events`；Provider Streaming 和 UI 广播等待 Provider 网络边界稳定后统一，不在本决策中新增 Pub/Sub 或第二套 Streaming Event。

## 后果

- Tool Call、Tool Result 和最终回答具备确定顺序和持久化恢复点；
- 已成功 Tool 的恢复不会重复执行，也不会重复消费 Grant；
- 状态不明的 Tool 会阻塞恢复而不是猜测；
- Agent Runner 现在必须配置 `RecordStore`，不存在无持久化备用路径；
- 新表是 append-only，增长控制、摘要和归档需要后续基于实际 Profile 决定；
- 当前恢复边界覆盖 Run 内执行记录，不等同于完整的交互式 Approval 等待恢复。
