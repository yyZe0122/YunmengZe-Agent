# 工具

全部模型工具经 [Registry.Execute](../../internal/tools/registry.go) 执行，列表过滤和执行入口都检查模式。每次调用外层超时 10 分钟；文本结果最多 64000 字节；普通 JSON 超限时保留首尾并包裹 `truncated: true`。图片作为独立内容块，不混入文本截断。必填、未知字段和 null 在执行前校验。

可选能力按[配置开关](configuration.md)注册，禁用后执行入口也无法找到对应工具。RootOnly 元数据同时控制列表与执行入口，阻止子会话创建下一层任务。

下表括号内为可选参数，其余为 schema 必填字段；图像 path / url 由处理器要求二选一。「两者」指 plan 和 agent。

## 文件、命令与网页

| 工具 | 参数 | 模式 | 行为 |
| --- | --- | --- | --- |
| `read` | `path`；（`offset`、`limit`） | 两者 | 读 UTF-8 普通文件，最多 8 MiB；offset 从 1 开始，limit 默认 / 最多 2000 行；返回正文、总行数、SHA-256 |
| `write` | `path`、`content`；（`expected_sha256`） | agent | 新建或替换文件，最多 8 MiB，自动创建父目录，返回新哈希和有界差异预览 |
| `edit` | `path`、`old_text`、`new_text`；（`expected_sha256`、`all`） | agent | 精确替换；old_text 不能为空，零匹配或多匹配且未设 all 时失败；匹配兼容 LF / CRLF，保留 BOM 和未修改区域的换行 |
| `glob` / `grep` | `pattern`；（`path`、`limit`） | 两者 | glob 支持 `*`、`?`、`**`；grep 使用 Go 正则 |
| `shell` | `command`；（`timeout_seconds`） | agent | 工作目录内运行命令；默认 120 秒、最多 600 秒，返回合并输出和退出码 |
| `web_search` | `query`；（`limit`） | 两者；web 启用 | DDG / SearxNG / Tavily 搜索，默认 8 条、最多 20 条 |
| `web_extract` | `url` | 两者；web 启用 | HTTP(S) 获取网页并提取文字，不执行页面脚本 |

文件相对路径基于会话工作区，绝对路径可在工作区外，由 OS 权限决定。write / edit 的 expected_sha256 可用 read 返回值；写入使用同目录临时文件再重命名，内置文件修改在进程内串行，没有跨工具事务；不能锁住外部编辑器和 shell。修改预览最多 40 行删除 / 新增，最多 12000 字节。

glob / grep 的 path 默认 `.`，limit 默认 100，支持 1–1000，超界回到默认；最多遍历 10000 个普通文件，跳过 `.git`、`.cache`、`node_modules` 子目录，不解析 .gitignore。grep 跳过大于 1 MiB、不可读或非 UTF-8 文件，单条正文保留最多 1000 字节。

shell 在 Windows 优先用 pwsh，回退 powershell.exe；其他系统用 sh。合并输出保留开头 / 结尾各 24000 字节，中间截断；运行中最多每 250 毫秒发送一次有界进度，非零退出码作为结果返回，超时 / 取消附加 error。网页请求超时 30 秒，搜索 / 提取响应上限为 2 / 4 MiB，提取正文最多 24000 字符；URL 必须是无 userinfo 的绝对 HTTP(S) 地址。

当前共用 HTTP 读取函数在超限时直接截掉尾部，不返回超限标记；网页正文因此可能不完整，结构化搜索响应可能解码失败。这与 Registry 对最终文本结果提供的 `truncated` 标记是两个不同阶段。

源码：[文件](../../internal/tools/files.go)、[shell](../../internal/tools/shell.go)、[网页](../../internal/tools/web.go)、[搜索后端](../../internal/tools/web_helpers.go)、[网页与图片共用的 HTTP 请求](../../internal/tools/http.go)。

## 技能、问答、记忆与子任务

| 工具 | 参数 | 模式 / 注册条件 | 用途 |
| --- | --- | --- | --- |
| `skills_list` | 无 | 两者；skills 启用 | 列出技能 ID、描述和来源 |
| `skill_view` | `id`；（`file_path`） | 两者；skills 启用 | 读取[技能正文或目录内文件](skills.md) |
| `ask_user` | `question`；（`options`、`multiple`） | 两者；仅 TUI | 非空问题，最多 12 选项，同步等待回答，多个子任务问题在 TUI 排队 |
| `memory_search` | `query` | 两者；记忆启用 | 查当前会话与全局有效事实，最多 20 条；空 query 列表 |
| `task` | `prompt`；（`kind`、`task_id`、`independent`） | 两者；task 启用且仅父会话可用 | 新建或续接叶子子会话，默认 kind=general |

task 的 kind 可选 general / explore / web；关闭 web 后 schema 不提供 web，恢复已有 web 子会话也会明确拒绝。explore 固定 plan，其余继承父模式。续接只能使用当前父会话的 task_id，不能改 kind。同步返回 task_id、result 和可选 error；子会话消息独立保存，不能再调用 task。只有较大、目标独立、文件不重叠的任务才应设 independent=true。连续独立 task 最多两个并行，结果按调用顺序入库；子结果文字最多 10000 字节，主 Agent 统一收尾。模型选择见[配置](configuration.md)。

源码：[装配入口](../../internal/agent/features.go)、[技能](../../internal/agent/feature_skills.go)、[记忆](../../internal/agent/feature_memory.go)、[子会话](../../internal/agent/feature_task.go)。

## MCP 与图片

| 工具 | 参数 | 模式 / 注册条件 | 用途 |
| --- | --- | --- | --- |
| `mcp_<服务名>` | （`tool`、`arguments`） | agent；MCP 启用且配置对应服务 | 省略 tool 列出远端工具，否则调用工具；arguments 默认 `{}` |
| `vision_analyze` | `path` / `url` 二选一；（`prompt`） | 两者；vision 启用，当前模型能力在调用时检查 | 本地图像最多 16 MiB，URL 图像最多读取 16 MiB |

MCP 在首次调用时连接，同一会话 / 服务复用连接；初始化限时 15 秒，调用失败后关闭失效连接；stdio 调用超时或取消会关闭管道、终止进程树并等待回收，Engine 退出时统一关闭。stdio 在会话工作区启动并应用配置的 env，远端支持 HTTP / SSE。Plan 不请求 MCP 工具列表。

远端 SSE 当前限制单行最多 4 MiB，尚无整帧累计上限。旧式 SSE 在已连接后断流，尚未收到响应的调用可能继续等待取消或工具超时，当前不会自动重连。URL 图像沿用共用 HTTP 截断逻辑，超过 16 MiB 时可能把不完整图片继续交给模型；本地文件则会返回超限错误。上述边界问题已列入[优化待办](../backlog/current.md)。

未配置 vision 时，支持图片的当前模型直接接收图片；不支持或能力未知时在工具调用中返回配置提示。配置独立 vision 时默认提示为 `Describe the image.`，分析文字返回主模型；独立模型已知不支持图片时，在运行装配阶段报错。Plan 中启用的搜索和图像工具仍可能联网。web 开关只控制内置网页工具和 web 子任务；不会阻止图像 URL、shell 或 MCP 自身联网。

源码：[MCP 复用](../../internal/agent/mcp.go)、[MCP stdio](../../internal/mcp/stdio.go)、[MCP 远端](../../internal/mcp/remote.go)、[图像工具](../../internal/tools/images.go)、[图像后端](../../internal/providers/vision.go)。
