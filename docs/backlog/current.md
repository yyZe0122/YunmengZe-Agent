# 当前审核与优化待办

基于当前工作区源码的静态 review，包含未提交修改。以下均为尚未实施的事项；优先级表示建议顺序，不表示已运行复现。P1 优先修复数据或资源风险，P2 修复具体行为与模块边界，P3 为可选简化。没有运行测试、构建或真实服务验证。

此文件是唯一维护中的优化清单，完成后移除条目。当前架构与实际拆卸范围见 [Wiki](../wiki/architecture.md)。模块重构的实施范围需确认；验收条件是后续工作目标，不是本轮验证结果。

## 需要优先处理的问题

### R1 · P1 · 工具结果延迟到整批结束才保存

- 位置：[agent/execution.go](../../internal/agent/execution.go) 的 `executeTools`。
- 触发：模型一次返回两个串行工具，第一个完成文件修改或外部操作，第二个运行较久，此时进程意外退出。当前结果先放在内存数组，全部调用结束后才逐条 `Append`；第一个已完成操作也没有持久化观察。
- 影响：恢复时只有 assistant 的调用记录，配对修复只能填“结果缺失”，主模型无法从历史判断哪个操作已完成，可能再次执行。正常取消有补写路径，进程退出不经过该路径。
- 最小方案：串行工具执行完立即保存；并行 task 只在自己的连续批次内收集、按原顺序保存，再执行后续工具。保存失败终止后续有副作用的调用，仍补齐可保存的观察。
- 完成条件：第二个工具开始前，第一个结果已经入库；并行结果顺序、取消收尾和错误传播保持一致。不承诺 OS 副作用与 SQLite 的跨系统原子性。

### R2 · P1 · MCP SSE 的整帧大小没有上限

- 位置：[mcp/remote.go](../../internal/mcp/remote.go) 的 `readSSEJSONRPCResponse`、`legacySSELoop`。
- 触发：服务端持续发送不含空行分隔符的多条 `data:` 行。Scanner 的 4 MiB 限制只约束单行，`dataLines` 可以持续增长，长连接尤其明显。
- 最小方案：为当前事件累计字节数，在追加前执行帧大小限制；超限返回明确错误、关闭该连接并结束等待。保留单行限制和请求超时。
- 完成条件：多条短行也不能绕过总帧上限；正常多行事件仍能解析。这是网络输入边界，不属于不可能触发的内部判空。

### R3 · P2 · MCP 的 JSON 响应没有核对请求 ID

- 位置：[mcp/remote.go](../../internal/mcp/remote.go) 的 `callStreamable` JSON 分支、`callLegacy` POST JSON 分支和 `decodeRPCResult`。
- 触发：服务端返回其他请求的响应时，Streamable HTTP JSON 分支直接解码，旧式 SSE 的 POST JSON 分支只检查存在 `id`，两者都不核对是否属于本次请求。无 `id` 的通知对象只有前者会直接送入解码；后者会继续等 SSE，不能混为同一种行为。`decodeRPCResult` 还把缺失结果序列化为 null 后返回成功。
- 边界：两个 SSE 事件分发路径已有 ID 匹配，不应重复增加匹配层；它们仍共用缺少结果结构校验的 `decodeRPCResult`。
- 最小方案：在响应边界核对 ID，并区分成功结果、远端 error 和无效响应；不为不匹配响应伪造空工具结果。
- 完成条件：不匹配 ID、通知对象和缺少响应字段均不能被当作本次工具成功；有效空结果按具体方法契约处理。

### R4 · P2 · URL 图片超限会被截断后继续发送

- 位置：[tools/http.go](../../internal/tools/http.go) 的 `doHTTP`、[tools/images.go](../../internal/tools/images.go) 的 `vision_analyze`。
- 触发：URL 返回超过 16 MiB 的图片。共用 HTTP 函数截掉尾部并返回 nil error；文件头仍能被识别为图片，因此不完整数据会继续发往 vision provider。本地图片路径则会明确报超限。
- 最小方案：有界 HTTP 读取显式返回超限结果；图片必须完整或失败，网页提取若保留截断则在结果中标识。不要再给网页和二进制数据共用隐式截断语义。
- 完成条件：图片 URL 与本地文件使用一致的完整性约束，网页截断可被调用方识别。

### R5 · P2 · 多进程同时迁移时检查与应用不在同一排他范围

- 位置：[store/sqlite/db.go](../../internal/store/sqlite/db.go) 的 `migrate`。
- 触发：两个 `ymz` 进程同时首次启动或升级，均在事务外读到同一迁移未执行；一个提交后，另一个仍可能执行已应用的 SQL 或重复登记版本并启动失败。会话租约在数据库打开之后获取，不能解决此问题。
- 最小方案：取得 SQLite 写锁后重新检查版本，再在同一事务内执行和登记；使用数据库自身的事务能力，不新增锁服务或另一份数据库。
- 完成条件：并发启动能够观察已提交迁移并跳过；单文件迁移失败仍完整回滚。保留所有已发布迁移。

### R6 · P2 · 技能发现阶段缺少元数据总量限制与文本校验

- 位置：[skillcatalog/catalog.go](../../internal/skillcatalog/catalog.go) 的 `readMetadata`，以及 [agent/loop.go](../../internal/agent/loop.go) 的 `prefix`。
- 触发：技能 frontmatter 包含大量短行，或 description 带隐藏控制字符。当前发现只限制单行大小；description 被直接拼入系统前缀，正文的扫描不会覆盖元数据。
- 最小方案：在发现阶段限制 frontmatter 总读取量，并用现有文本扫描器校验会注入的 ID / description。可疑词仍只诊断，不扩展为关键词权限系统。
- 完成条件：发现成本有界；元数据与正文的编码、控制字符规则一致，诊断仍能定位具体技能。

### R7 · P2 · 模型缓存先完整读入，再检查 2 MiB 上限

- 位置：[modelcatalog/refresh.go](../../internal/modelcatalog/refresh.go) 的 `Init`。
- 触发：本地缓存文件异常增大。`os.ReadFile` 在大小检查前就分配整个文件的内存，当前 2 MiB 规则没有限制读取成本。
- 最小方案：打开文件后最多读取上限加一个字节，再判断是否超限；保留有效缓存选择与内嵌回退规则。
- 完成条件：读取内存随既定上限增长，超限文件不会替换内嵌目录。无需引入缓存服务。

### R8 · P2 · CLI 输出失败后仍继续 Agent 执行

- 位置：[cmd/ymz/main.go](../../cmd/ymz/main.go) 的 `run` / `RunOptions.Emit`。
- 触发：首条 session 事件已成功输出，运行期间的输出仅返回 I/O 错误而未终止进程，例如 Windows 管道关闭、输出文件写满或其他失败的 Writer。当前只保存 `writeErr` 并跳过输出，`engine.Run` 继续模型调用和工具执行，直到整轮结束才返回输出错误。
- 边界：Unix 标准输出断管默认可能通过 SIGPIPE 直接结束进程，不能笼统声称所有平台的断管都会继续执行。`Engine.Run` 已串行化 Emit 回调，此项也不是并发 JSON 写入问题。
- 最小方案：为本轮建立可取消 context，在首次输出失败时取消；沿用 Engine 的工具收尾和租约释放，保留最初的写入错误。
- 完成条件：失去输出通道后不继续开始新的模型请求或有副作用工具；取消时仍保存必要观察。

### R9 · P2 · CLI 模型列表遗漏只出现在角色配置中的透传模型

- 位置：[providerconfig/load.go](../../internal/providerconfig/load.go) 的 `listModelRefsFromFile`，对照 [snapshot.go](../../internal/providerconfig/snapshot.go) 的模型收集。
- 触发：provider 没有显式模型目录，`models.subagent` 等角色引用另一模型。启动快照 / TUI 收集主模型、角色和目录模型，`ymz models` 只收集主模型及目录项。
- 最小方案：共享一个纯模型引用收集函数；CLI 列表继续支持离线、无密钥查询，不能直接改成必须通过完整启动校验。
- 完成条件：同一配置的模型引用集合一致，名称和排序稳定，列模型不会因未填写密钥失败。

### R10 · P2 · 旧式 MCP SSE 断流后未及时结束待响应调用

- 位置：[mcp/remote.go](../../internal/mcp/remote.go) 的 `legacySSELoop`、`callLegacy` 和 `Close`。
- 触发：旧式 SSE 已连接并完成初始化，某次 POST 已被接受，但对应响应到达前事件流 EOF 或读取失败。读取协程关闭 `sseDone`，`callLegacy` 却只等待请求 context 或 `respCh`；等待通道要到外部调用 Close 才被关闭，而 Agent 通常是在调用返回错误之后才清理连接。
- 影响：已经无法收到响应的调用仍占着本次工具，直到取消或外层期限到达；默认工具超时为 10 分钟，不能表述为永久阻塞。初始化阶段另有 15 秒限制。
- 最小方案：复用现有 `sseDone` 通知结束等待，保留读取失败原因并让 Agent 清理失效连接；不增加后台重连服务。处理结束通知与已排队结果的先后关系，避免丢掉已经收到的匹配响应。
- 完成条件：没有匹配响应的断流及时返回明确错误；有效响应优先交付，取消和连接回收仍完整执行。

## 分模块的最小优化方案

| 模块 | 待处理点与方案 | 完成边界 |
| --- | --- | --- |
| `cmd/ymz` | R8；保留入口、信号和生命周期职责，离线 sessions / db 操作可在同包按文件分离 | 不承载工具实现，不因无密钥阻塞离线管理 |
| `internal/tui` | 将技能、记忆的命令呈现与处理集中到各自文件；保留公共输入状态机 | 移除可选能力时，不需修改通用事件循环；不为单一前端新建接口体系 |
| `internal/agent` | R1；把可选能力的初始化、提示、运行结束与关闭调用集中到明确接入点 | 主循环保留请求、存储、工具执行与压缩顺序；不引入插件运行时 |
| `agent/feature_task` | 将 task 独立批次识别与调度集中；web kind 对 web 的依赖保留在组合层 | 移除 task 不留下工具名特判；子会话仍为叶子、继承模式、最多两个独立任务并行 |
| `internal/session`、`store/sqlite`、`migrations/core` | R5；会话拥有 SQL，SQLite 拥有连接与迁移 | 会话与记忆共享连接但不互查对方表；拆能力不删迁移和用户数据 |
| `internal/providerconfig` | R9；配置 DTO、纯校验与启动装配各守职责，统一模型引用收集 | `LoadSnapshot` 是唯一运行配置加载入口；离线列表不依赖凭证可用性 |
| `internal/providers`、`pkg/providerapi` | Chat 与其他适配器存在重复 HTTP 错误和 SSE 处理；仅归并相同传输机制，保留协议差异 | 适配器只返回统一消息；不能抽成通用协议解释器，不能删除签名 / replay 或有效兼容回退 |
| `internal/contextpack` | 减少 Pack / Repair 之间重复 ToolCalls 复制；`DroppedMessages` 当前按丢弃轮次递增，需统一命名或统计单位 | 不修改原始消息；完整工具配对和最近轮次保留；不得靠裁剪原生签名满足预算 |
| `internal/tools` | R4；保留已有 New 基础工具装配、Web / Images 独立注册入口；共用 HTTP 只负责传输与限额 | 不重复拆分已有入口或引入注册框架；运行限制仍统一在 Registry.Execute，具体工具保留输入语义校验 |
| `internal/skillcatalog` | R6；模板耦合实际位于 agent/feature_skills.go 的 expand，应在 Agent 层分开公共模板与技能展开；skillcatalog 本身不依赖 providerconfig | 移除技能仍能用配置模板与运行规则；关闭技能不扫描目录 |
| `internal/memory` | 将 curator、纠正 / 遗忘中的条目 SQL 收敛到 Store；评估把注入缓存收缩到单次运行作用域 | Initialize / Maintain / Shutdown 当前仍有生产调用，不能因删除旧写入接口而一并删除；证据、TTL、遗忘标记、历史查询继续有效 |
| `internal/mcp`、`agent/mcp.go` | R2、R3、R10；握手期间当前持有全 Engine 的 mcpMu，独立服务初始化也串行，应缩小到缓存操作；子会话连接保留至 Engine.Close 的生命周期需明确 | 网络等待不占整个连接缓存锁；子任务结束释放连接还是保留服务状态，实施前确认，避免擅自改变有状态工具行为 |
| `internal/modelcatalog` | R7；当前缓存“条目数不少于内嵌目录”的选择规则需单独确认是否保留 | 不凭空把条目数量等同更新时间；不新增定时刷新或后台服务 |
| `internal/injectscan`、`platform/pathsecurity` | 通过 R6 接入遗漏的元数据入口；继续区分词语诊断与文件访问边界 | 不添加 OS 账户权限之外的 workspace grant；保留真实文件变更、符号链接和路径检查 |
| 构建、发布、文档 | 若实施上述变更，同批更新 schema / 示例、相关 Wiki 与双语 README；发布仍经现有唯一脚本 | 不改发布产品范围、Go 依赖或 CI 策略来掩盖问题；`internal/version` 保持构建信息叶子包 |

## 拆卸方案与实施顺序

以源码可替换、依赖单向为目标。CLI / TUI、Agent 编排、会话持久化、Provider 协议与工具执行构成必要主干，不能删除后仍期待完整 Agent 运行；可选能力应能关闭，并能通过有限接入点从源码移除。

1. 先处理 R1–R10，各自形成可审查的小改动；不把行为修复混入文件搬迁。
2. 再收拢 `Engine` 中记忆 / MCP 的具体生命周期字段，以及主循环中的技能目录 / task 提示依赖。优先具体结构体和显式函数；只有消费方确实需要替换实现时才引入小接口。
3. 按能力逐项处理实现、装配、配置、角色依赖、提示、TUI 入口；能力之间的依赖只能放在组合层。保留 `web task` 依赖 task + web、独立 vision 依赖 provider 这些真实约束。
4. 最后核对文档和残留引用。运行测试、增加测试用例或执行真实服务验收，仍由用户明确安排；没有行为验证时，不把静态清理标记为运行验证通过。

测试清理按行为分类：当前配置、协议转换、取消、工具配对、FTS、TTL 和迁移数据可读性的断言有业务价值；只有已删除路径的专属测试、空断言和重复包装才可直接移除。测试调用的旧入口应改接生产入口，不为保留测试而保留另一套业务实现，也不通过删除有效断言掩盖缺陷。
