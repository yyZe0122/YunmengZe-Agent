# ADR-052：编码循环 Harness（turn / step / inbox）

- 状态：Accepted（**R1–R5 已落地**）
- 日期：2026-08-17
- 修订：ADR-004 / 008 / 012 / 018 / 038 / 039 / 041 / 043 / 051 后果段
- 更新：2026-09-03（Inbox `ClaimStep` 按 session+run；子循环不领取父 steer）
- 更新：2026-09-02（问题卡向导 / 自定义回答 / dismiss / Wait 30m）
- 更新：2026-09-07（父 ctx cancel：先落 `interrupted` 观察再取消 turn）

## 背景

Phase Q（ADR-051）把装配收成一个 `ContextView`。落地前循环语义仍断（均已修）：

- 非 `ErrDenied` 的工具失败（`go test` 非零、`fs_read` 缺文件、git 失败）会 `failChat`，模型看不到观察。
- 默认 TUI Agent 为 `/perm` 把 process/git **各写进 plan 两次**，`AllowedTools` 重名 → `duplicate allowed tool`，模型还没说话就死。
- 默认 16 步帽、无 mid-turn steer、`http_get` 幽灵、技能默认不进 Prefix。

对标 OpenCode / Claude / Codex / DeepSeek spine：**失败是观察，不是整轮死亡**。不上 Cordis、不上 Code Mode、不改三件套。

## 决策

循环合同（分 R1–R5 落地，不可只合半截观察）：

```text
turn 开
  ContextView.Build（ADR-051 不动）
  step = 一次模型请求 + 它点的工具
    工具失败 → JSON 观察 → 继续
  有 tool_calls 或 inbox.next-step → 下一 step
  模型停且 inbox 空 → turn 关
人在跑时回车 → steer（next-step），不取消当前工具
Esc / /new → 取消 turn（清 inbox）
闲着回车 → 新 task（不经 inbox）
```

Inbox **只有 next-step**。没有 next-turn 队列：空闲回车走现有 `SubmitTask`。

终止只剩：模型自己停、Esc、30min、token/cost、loop-detect、空回复。默认不再因步数帽整轮失败（R2）。turn 终态（complete / fail / cancel / pause 退出）必须 `Inbox.Clear(session)`，避免 steer 串到同 session 下一轮。

工具哲学：保持 typed `fs_*` / `process_*` / git / todo / skill。Plan / Agent / Auto 三档语义不变。

### 观察合同（R1，必须先合）

| 结果 | 循环 |
| --- | --- |
| 成功 | tool JSON，继续 |
| 策略拒绝 / 人 deny / CLI 无 wait | `tool_denied` JSON，继续 |
| 工具业务失败（非零退出、缺文件、patch miss、git/MCP/HTTP 失败、超时） | `{error, tool, message, hint?}` 或工具自带 JSON，**继续** |
| 未广告 / 非法 tool call | `unadvertised_tool` / `invalid_tool_call`，继续 |
| 父 ctx cancel（Esc / `/new` / 权限等待结束） | 先落 `interrupted` JSON（in-flight + 同 step 未执行 sibling），再取消 turn。packing 不再补 `orphan_tool_call` |
| 基础设施（DB 无法落盘） | 失败 turn |

`process_exec` / `process_shell` / `git_*`：**非零退出是成功结果**（`exit_code` / stdout / stderr），不是 Go error。启动失败（找不到命令、坏工作目录）同样编成观察 JSON，除非父 ctx 已取消。Broker 工具超时改写成 `ErrToolTimeout` 后走观察，不是父 ctx 超时。

`AllowedTools` 按名 unique（先到先得）。ask 姿态仍可在 plan 里保留 once + session **两条 CapabilityScope**（`/perm` decide 需要），但广告名只出现一次。

### R2（已落地）

- `Runner.Run` 按 **step** 循环：一次模型请求 + 其工具（或文本停）。
- 步间 / 文本停后 `Inbox.ClaimStep(session, runID)`：只领取该 run 的 next-step（子循环不领取父 steer）。未 `Persisted` 的落 `agent_run_records`，再开下一步。
- `max_iterations`：**省略 / 0 = 不硬顶**；1–256 时最后一步 soft-landing（摘工具 + 文本）。
- `Inbox` 是进程内 next-step 队列；`Steer(session, runID, …)` 挂在 `Runner.Inbox()`。Gateway + TUI 回车见 R3。

### R3（已落地）

- `POST /v1/sessions/{id}/steer` `{text}`：先写入父 chat run 的 `agent_run_records`（user），再 `Inbox.Enqueue`（`Persisted: true`，`RunID` = 父 chat run）。子 `task` 循环不 Claim 这条。
- 仅当 session 有 `running` task 且 chat run 已创建；空闲 → conflict。
- TUI：仅当**当前** task 已是 `running`（乐观绘制之前判定）才走 steer；空闲/已结束会话回车提交新一轮 chat。steer 若 409（回合刚好结束）自动改走 submit，status `turn ended · sent as new message`。`/new` / Esc 仍取消并清 inbox。

### R4（已落地）

- 新 builtin `ask_user`（R0，plan+agent 都广告）。交互 TUI 挂起等答；CLI/cron 立刻 `{error:unavailable}`。
- `user_questions` 表（migration 027）+ `internal/userquestion` waiter，与 `/perm` 分开。
- `GET /v1/questions` · `POST /v1/questions/{id}/answer` · `POST /v1/questions/{id}/dismiss`。TUI 问题卡：perm 优先；多题向导；每题末项 Type your own answer；1–9 / Space / Enter；Esc Esc（3s）撤销整组。自定义回答在有 options 时也接受。Wait 30m。SSE：`question.pending` / `question.answered`。

### R5（已落地）

- Prefix 注入活跃技能 **目录**（id — 一句话）；正文仍 `skill_view`。`injectscan` 失败则跳过该项。
- `http_get` / `web_search` / `web_extract` 进交互 agent plan（once+session 双 scope，不预发）。`/perm similar` 把请求 host 写入 grant；`planContainsScope` 允许空域名 plan → 单 host 收窄。**Plan / cron 不广告。**
- 子代理 Prefix = 短身份 + 同一套 AGENTS.md（`providerconfig.OverlayAgents`）。不继承父 Tail / 记忆 / 技能正文。

### 不做

Cordis / 通用事件总线 / 默认 Code Mode / bash-only 重写。不合并 `contextpack` / `chatsession` / `agent`。不改 `fs_*` 名。不做第二套 `apply_patch`。不做 next-turn inbox。Plan 只读、cron/CLI fail-closed、禁止 yolo。

## 后果

- 编码循环的「试 → 败 → 改」成立：测试失败与补丁未命中不再掐死 turn。
- 默认交互 Agent 可以真正开局（不再 duplicate advertise）。
- 默认不再因步数帽拆轮；配置了帽也是 soft-landing。
- 运行中回车进入当前 turn 的下一步，不取消进行中的工具；取消/结束必须清 inbox。
- 模型可停下来问选择题；无交互面时循环继续。
- 冷启动能看见技能目录；交互 agent 能调 `http_get`（须 `/perm`）；cron/plan 看不到该工具；子代理遵守项目规则。
