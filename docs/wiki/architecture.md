# 架构与上下文

`ymz` 是唯一程序。CLI / TUI 在同一进程直接调用 `agent.Engine`；Engine 组合模型、工具、会话和可选记忆，并拥有配置目录下唯一业务数据库 `core.db` 的连接。

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

## 模块边界与可选能力

Engine 的会话 Store 和 Memory Manager 为私有成员。CLI / TUI 使用[会话操作](../../internal/agent/sessions.go)和[记忆操作](../../internal/agent/feature_memory.go)，不持有存储实现。离线 sessions / db 管理命令按需打开短生命周期数据库，不依赖模型密钥。

[features.go](../../internal/agent/features.go)是显式装配入口；文件和 shell 是基础工具，其余能力分别装配。关闭开关不会删除数据或迁移历史，也不改变 plan 的执行限制。

| 能力 | 装配与边界 | 关闭后的行为 |
| --- | --- | --- |
| 记忆 | [feature_memory.go](../../internal/agent/feature_memory.go)；通过 memory.EvidenceReader 读取由 [session](../../internal/session/evidence.go)提供的用户证据 | 不初始化、查询、注入、整理或维护 |
| 技能 | [feature_skills.go](../../internal/agent/feature_skills.go)；每次运行复用一次发现结果 | 不扫描目录、注册工具、提供补全或展开技能 |
| 子任务 | [feature_task.go](../../internal/agent/feature_task.go)；并行规则仍属于 Agent | 不注册 task，不注入委派提示 |
| Web | [feature_web.go](../../internal/agent/feature_web.go)；工具只接收自身 WebConfig | 不注册搜索 / 提取，也不创建或恢复 web 子任务 |
| 图像 | [feature_vision.go](../../internal/agent/feature_vision.go)；Agent 转换 provider 结果为工具结果 | 不注册图像工具，不依赖独立 vision 模型 |
| MCP | [mcp.go](../../internal/agent/mcp.go)；按会话 / 服务缓存连接，由 Engine 统一关闭 | 不注册工具、不解析专用依赖或连接服务 |
| 自动压缩 | [compact.go](../../internal/agent/compact.go)与主循环 | 不自动摘要或超限重试；手动压缩和基础打包仍可用 |

启动开关控制初始化和注册，不等于源码已完全独立。当前 [Engine](../../internal/agent/engine.go) 仍直接持有 memory.Manager、MCP 连接与关闭逻辑；[主循环](../../internal/agent/loop.go)仍引用技能目录、记忆和 task 提示；[工具调度](../../internal/agent/execution.go)仍识别 task 的独立批次。CLI / TUI 还读取 Engine 暴露的配置、角色和模型目录。上述接入点都是当前实际依赖，移除能力时需一并调整，不支持运行中卸载。

| 从源码移除的能力 | 需要同步处理的接入点 | 保留的主干 |
| --- | --- | --- |
| 记忆 | feature_memory、Engine 初始化 / 关闭、运行开始 / 结束钩子、memory 角色和配置、TUI memory 命令 | 会话保存、摘要、全部历史迁移和用户数据 |
| 技能 | feature_skills、主循环目录与前缀、slash 展开和补全、skills 配置；[feature_skills.go](../../internal/agent/feature_skills.go)中的配置模板需与技能分开处理 | 运行规则、chat.commands、文件工具 |
| 子任务 | feature_task、执行器独立批次调度、委派提示、subagent / web 角色依赖、子任务事件呈现 | 根会话运行与现有子会话数据 |
| Web | feature_web、tools 中搜索 / 提取实现、web 配置、task 的 web kind | 图片仍需使用共用 HTTP 请求，shell / MCP 联网不受此开关控制 |
| 图像 | feature_vision、tools 图片入口、providers 图像调用、vision 配置和角色依赖 | Provider 的历史消息解析、已保存图片与原始协议状态 |
| MCP | agent/mcp、Engine 连接字段与关闭逻辑、internal/mcp、MCP 配置和引用值解析 | 本地工具注册与执行 |
| 自动压缩 | 主循环触发条件、超限重试、对应配置和角色依赖判断 | 基础上下文打包；手动压缩仍需要 compact 实现和摘要存储 |

必要主干也有职责边界，但不能删除后仍保持完整 Agent 功能：CLI / TUI 负责交互，Agent 负责用例编排，session 负责会话持久化，providers 负责模型通信，tools 负责执行。替换这些实现时必须保留调用方实际使用的契约；不为“可拆卸”引入动态模块系统或为每个类型预建接口。待实施的解耦步骤见[当前审核与优化待办](../backlog/current.md)。

接口只用在实际边界：memory 消费 EvidenceReader，由 session 提供会话证据；tools 消费 Vision 接口，由 Agent 连接模型后端。providers 不依赖 tools，tools 不依赖 providerconfig，memory 不查询会话表。会话与记忆共用 Engine 打开的数据库，各自拥有本模块 SQL。跨能力依赖由 Agent 组合：web 子任务同时需要 task / web，独立 vision 需要 provider；网页与图片共用的[有界 HTTP 请求](../../internal/tools/http.go)独立于搜索实现。

内部依赖在构造和装配时确定；必要校验保留在用户参数、配置、文件、进程和网络响应入口。存储层的 FTS 读写错误返回调用方，不把缺表或损坏静默降级为 LIKE 查询。配置开关见[配置说明](configuration.md)。

## 执行流程

[主循环](../../internal/agent/loop.go)获取会话租约 → 保存用户输入及排队纠偏 → 构建上下文 → 请求模型并保存响应 → 执行工具并保存结果。串行工具完成后立即入库，再开始下一个；连续独立 task 在本批次内并行，批次结束后按调用顺序保存，再执行后续工具。保存失败时停止后续调用，尝试补齐未执行原因并返回保存错误。外部操作与 SQLite 保存不构成跨系统原子事务；工具配对修复不能判断缺少观察的操作是否已完成。还有工具调用或纠偏时继续，否则完成本轮并按条件整理记忆。

模式只有 agent / plan。Agent 按 OS 账户权限直接执行，无审批门；plan 在工具列表和执行入口都限制写入能力，仍保存会话、摘要并可联网。具体可用工具见[工具表](tools.md)，权限边界见 [SECURITY.md](../../SECURITY.md)。

[子会话](../../internal/agent/feature_task.go)通过 task 执行，共享 Engine 和工作目录，但不继承父会话完整消息；父会话只接收结果。explore 固定 plan，其余继承父模式；所有子会话都是叶子。同一模型批次中连续且显式标为 independent 的 task 使用两个 worker 调度；普通工具和依赖任务串行。独立性由主模型判断，要求目标独立且修改文件不重叠；内置文件写入串行并保留哈希冲突检查，共享目录并不提供 shell / 编辑器事务隔离。主 Agent 负责整合。模型回退规则见[配置](configuration.md)。

## 上下文与压缩

发给模型的顺序是系统前缀 → 已有摘要 → 摘要锚点之后的消息。运行开始读取规则，按开关发现技能、刷新用户偏好注入，之后固定前缀；工具定义按名称稳定排序。消息从数据库按 ID 增量读取。本地 token 估算含原始协议状态与图片预算，只用于控制上下文，并非计费数值。

[上下文打包](../../internal/contextpack/pack.go)生成独立的工具配对视图、截短大段正文、按正文与图片的实际可回收估算清理较早工具输出，并在超预算时移除最早完整轮次。裁轮时按被移除内容递减估算量，确定范围后一次整理消息切片；保留系统前缀、前置原生压缩窗口和至少最后一轮，仍超限则报错。正文截断按提示文字预留预算，提示中的省略字符数对应实际删除数量。只改变模型视图，不删数据库消息。

[摘要压缩](../../internal/agent/compact.go)默认在估算量超过可用预算 75% 时触发，也可 `/compact [重点]` 手动执行。按 token 预算保留约三分之一可用窗口的近期消息，并只在完整工具批次之间切分；单个很长的用户轮次也能压缩。没有可分离的较早内容则跳过。采用 compact 角色，默认请求上限 2048 tokens、90 秒超时。

Responses 模型显式开启 nativeCompaction 时优先使用原生压缩，整个返回窗口保存到 summary_replay；原生调用失败且操作未取消时使用通用摘要，取消或父 context 到期后直接结束。原始签名和原生压缩窗口不做字符串裁剪。

自动压缩失败后继续基础打包；开启自动压缩时，模型报告上下文超限可尝试一次压缩后重试。关闭自动压缩不影响手动压缩和基础打包。

## 错误与取消

工具错误作为观察保存，供模型下一轮处理；provider 在未交付任何流事件前可进行有限重试，已输出的部分内容会在中断时保存。[StreamAccumulator](../../pkg/providerapi/provider.go)分别累积正文与思考增量，在完成或中断时读取字符串，使用后不复制累加器。主循环会话读写失败会结束本轮；CLI 输出写入失败会取消本轮并返回原始写入错误。记忆整理失败报告 info，维护失败记日志，注入查询失败时跳过本轮注入，不把这些辅助失败当作主任务失败。

取消会尝试为已保存工具批次补齐取消观察，终止 shell / 正在调用的 MCP stdio 进程树，等待请求取消回调及租约续期退出后释放租约；主循环与手动压缩共用[续期生命周期](../../internal/agent/lease.go)。Engine 退出时取消并等待全部运行、关闭复用的 MCP 连接，再关闭数据库。租约参数和持久化规则见[数据库](database.md)。

TUI 将子 task 事件与主回复分开显示，问题按队列处理，流式重绘最多每 50 毫秒合并一次。主调用、子任务、图像、自动压缩和记忆整理通过事件上报 token 用量；手动 `/compact` 当前不回传用量事件。显示的用量依赖服务返回，不推算未提供的金额。源码：[执行与重试](../../internal/agent/execution.go)、[MCP 生命周期](../../internal/agent/mcp.go)。
