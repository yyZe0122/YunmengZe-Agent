# 架构与上下文

`ymz` 是唯一程序。CLI / TUI 在同一进程直接调用 `agent.Engine`；Engine 组合模型、工具、会话和可选记忆，并拥有唯一业务数据库 `core.db` 的连接。

## 代码对应关系

| 源码 | 职责 |
| --- | --- |
| [cmd/ymz](../../cmd/ymz/main.go) | CLI、配置目录、信号与 Engine 生命周期 |
| [internal/tui](../../internal/tui/) | 输入、选择器、问答、消息呈现；不自行执行工具或连接数据库 |
| [internal/agent](../../internal/agent/) | 装配、模型循环、可选功能、子会话与压缩 |
| [internal/session](../../internal/session/store.go) | 会话、消息、摘要与租约 |
| [internal/tools](../../internal/tools/) | 工具注册、模式检查、文件、shell、web、图片 |
| [internal/providers](../../internal/providers/)、[pkg/providerapi](../../pkg/providerapi/) | 协议适配、统一消息、流事件、用量和错误 |
| [internal/providerconfig](../../internal/providerconfig/) | 配置、密钥、角色、规则和模板 |
| [internal/contextpack](../../internal/contextpack/) | token 估算、工具配对、上下文裁剪 |
| [internal/skillcatalog](../../internal/skillcatalog/catalog.go) | 技能发现与按需读取 |
| [internal/memory](../../internal/memory/) | 用户偏好、证据、运行内固定注入、TTL 和低频整理 |
| [internal/mcp](../../internal/mcp/) | stdio、HTTP / SSE 客户端 |
| [internal/modelcatalog](../../internal/modelcatalog/) | 内嵌模型元数据、缓存与显式刷新 |
| [internal/store/sqlite](../../internal/store/sqlite/db.go)、[migrations/core](../../migrations/core/) | SQLite 生命周期与有序事务迁移 |
| [internal/injectscan](../../internal/injectscan/scan.go)、[internal/platform/pathsecurity](../../internal/platform/pathsecurity/security.go) | 指令扫描、规则与技能路径检查 |
| [internal/version](../../internal/version/version.go) | 构建版本信息 |

## 执行流程

[主循环](../../internal/agent/loop.go)获取会话租约 → 保存用户输入及排队纠偏 → 构建上下文 → 请求模型并保存响应 → 执行工具并按调用顺序保存结果。还有工具调用或纠偏时继续，否则完成本轮并按条件整理记忆。

模式只有 agent / plan。Agent 按 OS 账户权限直接执行，无审批门；plan 在工具列表和执行入口都限制写入能力，仍保存会话、摘要并可联网。具体可用工具见[工具表](tools.md)，权限边界见 [SECURITY.md](../../SECURITY.md)。

[子会话](../../internal/agent/features.go)通过 task 执行，共享 Engine 和工作目录，但不继承父会话完整消息；父会话只接收结果。explore 固定 plan，其余继承父模式；所有子会话都是叶子。同一模型批次中连续且显式标为 independent 的 task 使用两个 worker 调度；普通工具和依赖任务串行。独立性由主模型判断，要求目标独立且修改文件不重叠；内置文件写入串行并保留哈希冲突检查，共享目录并不提供 shell / 编辑器事务隔离。主 Agent 负责整合。模型回退规则见[配置](configuration.md)。

## 上下文与压缩

发给模型的顺序是系统前缀 → 已有摘要 → 摘要锚点之后的消息。运行开始读取规则和技能目录、刷新用户偏好注入，之后固定前缀；工具定义按名称稳定排序。消息从数据库按 ID 增量读取。本地 token 估算含原始协议状态与图片预算，只用于控制上下文，并非计费数值。

[上下文打包](../../internal/contextpack/pack.go)修复工具调用配对、截短大段正文、清理较早工具输出，并在超预算时移除最早完整轮次；保留系统前缀和至少最后一轮，仍超限则报错。只改变模型视图，不删数据库消息。

[摘要压缩](../../internal/agent/compact.go)默认在估算量超过可用预算 75% 时触发，也可 `/compact [重点]` 手动执行。按 token 预算保留约三分之一可用窗口的近期消息，并只在完整工具批次之间切分；单个很长的用户轮次也能压缩。没有可分离的较早内容则跳过。采用 compact 角色，默认请求上限 2048 tokens、90 秒超时。

Responses 模型显式开启 nativeCompaction 时优先使用原生压缩，整个返回窗口保存到 summary_replay；失败使用通用摘要。原始签名和原生压缩窗口不做字符串裁剪。

自动压缩失败后继续基础打包；开启自动压缩时，模型报告上下文超限可尝试一次压缩后重试。关闭自动压缩不影响手动压缩和基础打包。

## 错误与取消

工具错误作为观察保存，供模型下一轮处理；provider 在未交付任何流事件前可进行有限重试，已输出的部分内容会在中断时保存；数据库错误结束本轮。取消会尝试为已保存工具批次补齐取消观察，终止 shell / 正在调用的 MCP stdio 进程树，释放租约；Engine 退出时取消并等待全部运行、关闭复用的 MCP 连接，再关闭数据库。租约参数和持久化规则见[数据库](database.md)。

TUI 将子 task 事件与主回复分开显示，问题按队列处理，流式重绘最多每 50 毫秒合并一次。主调用、子任务、图像、压缩、记忆整理都上报 token 用量；显示的用量依赖服务返回，不推算未提供的金额。源码：[执行与重试](../../internal/agent/execution.go)、[MCP 生命周期](../../internal/agent/mcp.go)。
