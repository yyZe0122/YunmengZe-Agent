# 配置与模型

源码：[类型](../../internal/providerconfig/types.go)、[启动快照](../../internal/providerconfig/snapshot.go)、[解析](../../internal/providerconfig/resolve.go)、[chat 设置](../../internal/providerconfig/settings.go)。另见[配置示例](../../configs/agent.json.example)和 [JSON Schema](../../configs/agent.schema.json)。

## 加载与密钥

- 配置目录为 `YMZ_HOME` 或 `~/.yunmengze`，相对路径基于启动目录；不从项目目录读取 provider 配置。
- 优先 `agent.local.json`，其次 `agent.json`，两者不合并。JSON 解码最多读取 1 MiB，拒绝未知字段和多个顶层值。
- 可选 `env` 使用 `KEY=value`，支持行首注释、`export ` 和一层引号，不执行 shell 展开；非空进程环境变量优先。
- provider 的 `apiKey` / `headers`、MCP 的 `env` / `headers`、当前搜索后端的 `searxng_url` / `tavily_key` 支持整个值为 `{env:NAME}` 或 `{file:path}`。相对文件基于配置目录，引用值缺失或为空报错；不支持嵌入字符串。认证头的引用值应包含完整 `Bearer ...`。
- 修改 JSON 或密钥后重启。启动一次读取 JSON、环境和密钥引用，固定为 Engine 快照；以后首次使用其他模型也不重读文件。适配器按模型引用惰性创建。[规则和技能](skills.md)在每次运行开始发现，正文按需读取。

## 顶层与 provider

| 顶层字段 | 用途 |
| --- | --- |
| `$schema` | 编辑器提示，程序不下载 schema |
| `model` | 新会话默认模型 `provider/model` |
| `provider` | 按 ID 命名的连接与模型目录 |
| `models` | 可选模型角色，见下表 |
| `chat` | 压缩、记忆、模板和搜索设置 |
| `mcp` | MCP 服务 |

模型引用只按第一个 `/` 分隔：`router/vendor/model` 对应 provider `router`、模型 key `vendor/model`。provider 模型目录为空时透传模型段；非空时必须命中目录。

| `provider.<id>` 字段 | 含义 / 默认值 |
| --- | --- |
| `protocol`、`type` | [协议及别名](provider-protocols.md)；默认 `openai-chat`，同时填写时必须等价 |
| `options.baseURL` | 必填，绝对 HTTP(S) API 基址 |
| `options.apiKey` | 可选密钥，认证方式由协议决定 |
| `options.completionPath` | 覆盖默认请求路径 |
| `options.responseFormat` | 仅 Chat：`auto`（默认）、`json_schema`、`json_object` |
| `options.anthropicVersion` | Anthropic 版本头，默认 `2023-06-01` |
| `options.headers` | 自定义请求头，覆盖同名默认头 |
| `models` | 以下模型目录项 |

| `provider.<id>.models.<key>` 字段 | 含义 / 默认值 |
| --- | --- |
| `name`、`id` | 显示名、实际 API 模型 ID；id 省略时用引用的模型段 |
| `responseFormat` | 覆盖 provider 的 Chat 格式设置 |
| `temperature` | 0–2，省略时不传 |
| `maxTokens`、`limit.output` | 非负输出上限；maxTokens 为 0 才采用别名，均为 0 则取目录上限、16384 和窗口四分之一的最小值；目录无输出上限时用 4096 |
| `contextWindow`、`limit.context` | 非负上下文窗口；contextWindow 为 0 才采用别名，均为 0 则查模型目录 |
| `reasoningEffort` | 仅 Chat / Responses；取值由服务支持情况决定 |
| `supportsImages` | 可选布尔值，覆盖图像输入能力；省略时仅采用精确模型 ID / 一致的精确模型段元数据 |
| `nativeCompaction` | 默认 false；仅 Responses，确认服务支持 `/responses/compact` 后设 true |

目录查询使用实际模型 ID，未命中时窗口回退为 32768 tokens；应按服务限制显式填写。窗口用于本地估算，不发送给 API。输出上限必须小于窗口；未知模型的默认值是保守预算，不代表服务实际能力。参数映射和结构化输出限制见[协议说明](provider-protocols.md)。

## 模型角色

`models` 中的值为 `provider/model`，空值视为未配置；不接受 `models.main`。

| key | 用途 | 未配置时 |
| --- | --- | --- |
| `subagent` | general / explore 子会话 | 父会话模型 |
| `compact` | 通用摘要压缩 | 当前会话模型 |
| `memory` | 低频用户偏好整理 | 当前会话模型 |
| `web` | web 子会话 | subagent → 父会话模型 |
| `vision` | 独立图像分析 | 当前会话模型原生接收图片；能力未知或不支持时提示配置 |

`web` 角色不改变搜索后端。续接子会话保留其模型；CLI / TUI 模型选择规则见[使用说明](usage.md)。

## chat

以下字段相对于 `chat`；均可省略，模板项的 template 除外。

| 字段 | 默认值 | 约束 |
| --- | --- | --- |
| `max_iterations` | `0` | 0 不限；1–256 限制单次提交的模型循环次数 |
| `compaction.enabled` | `true` | 自动压缩和超限重试；不关闭基础打包与手动压缩 |
| `memory.enabled` | `true` | 记忆工具、注入、整理和维护总开关 |
| `memory.max_inject_runes` | `2000` | 0 用默认；非零 200–16000 |
| `memory.default_ttl` | 空 | 偏好不默认过期；可设正 Go duration，最多 `8760h`，如 `720h` |
| `memory.curator.enabled` | `true` | 自动整理用户偏好，受 memory 总开关限制 |
| `memory.curator.max_facts` | `3` | 0 用默认；非零 1–8 |
| `memory.curator.min_turns` | `8` | 至少累计多少条直接用户消息；0 用默认，非零 1–64 |
| `memory.curator.min_interval` | `30m` | 整理尝试的最小间隔；1m–720h，失败也遵守间隔 |
| `memory.curator.timeout_ms` | `15000` | 0 用默认；非零 1000–120000 毫秒 |
| `commands.<name>.description` | 空 | 模板说明 |
| `commands.<name>.template` | 必填 | 非空，最多 8000 字符；见[模板规则](skills.md) |
| `web.search` | `ddg` | `ddg`、`searxng`、`tavily` |
| `web.searxng_url` | 空 | searxng 必填 HTTP(S) 基址，无 userinfo；末尾追加 `/search`，勿带尾斜杠或查询参数 |
| `web.tavily_key` | 空 | tavily 必填密钥 |

整理频率是可调的成本控制参数，不是偏好真实性的判断门槛。偏好采纳规则见[记忆](memory.md)。

## MCP

每个服务位于 `mcp.servers.<name>`，名称只含字母、数字、`_`、`-`。

| 字段 | 用途 |
| --- | --- |
| `type` | `stdio`、`http`、`sse`、`remote`；运行时另接受 local 作为 stdio 别名；省略时有 command 则 stdio，仅有 url 则 remote |
| `command`、`args`、`env` | stdio 的程序（必填）、参数数组、环境覆盖值 |
| `url`、`headers` | 远端必填的绝对 HTTP(S) URL、请求头 |

`http` 使用 Streamable HTTP；`sse` 使用 HTTP+SSE；`remote` 先尝试前者，仅在确认不受支持时回退。首次调用时连接，按会话 / 服务复用，失败连接失效，Engine 退出时关闭；连接初始化限时 15 秒。plan 禁止连接和调用，见[工具](tools.md)。

## 模型元数据

启动从 `<config>/cache/models.dev.json` 或内嵌快照读取窗口等限制，不联网刷新。缓存须能解析、不超过 2 MiB、条目数不少于内嵌目录。`ymz models refresh` 显式下载并尝试保存缓存，不改 provider 模型配置、不验证密钥；`ymz models` 只列配置模型。

源码：[模型目录](../../internal/modelcatalog/)、[刷新缓存](../../internal/modelcatalog/refresh.go)；维护内嵌快照使用 [refresh-modelcatalog.sh](../../scripts/refresh-modelcatalog.sh)。
