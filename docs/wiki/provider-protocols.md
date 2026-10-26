# Provider 协议

源码：[工厂](../../internal/providers/factory.go)、[统一接口](../../pkg/providerapi/provider.go)、[HTTP 路径与请求头](../../internal/providers/internal/providerhttp/http.go)。配置字段见[配置与模型](configuration.md)。

## 支持范围

| protocol / 实现 | 默认路径 | apiKey 认证 | 实际流式输出 | 图像输入 |
| --- | --- | --- | --- | --- |
| [openai-chat](../../internal/providers/openai/provider.go) | `/v1/chat/completions` | Bearer | SSE：正文 / 思考增量，工具调用收齐后交付 | 支持 |
| [openai-responses](../../internal/providers/openairesponses/provider.go) | `/v1/responses` | Bearer | SSE：正文 / 推理摘要增量，终态确认工具调用 | 支持 |
| [anthropic-messages](../../internal/providers/anthropic/provider.go) | `/v1/messages` | x-api-key | SSE：内容块 / 思考增量，终态确认工具调用 | 支持 |
| [gemini-generate-content](../../internal/providers/gemini/provider.go) | `/v1beta/models/{model}:generateContent` | x-goog-api-key | streamGenerateContent SSE：文本 / 思考增量 | 支持 |

四者均支持工具调用转换，默认响应头等待上限 60 秒、请求总时限 10 分钟，调用方可设更短期限；非流式响应上限 4 MiB。统一事件为 delta、thinking_delta、tool_call、complete，由 Engine 转成[前端事件](usage.md)；用量包含输入、输出、总 token 及服务返回的缓存读写量。

protocol 推荐使用上表规范名，与 JSON Schema 一致。运行时 protocol / type 也接受以下别名，不区分大小写；都省略时默认 Chat，同时填写必须等价：

| 协议 | 别名 |
| --- | --- |
| Chat | `openai-chat-completions`、`chat-completions`、`openai-compatible`、`openai-compat`、`ollama`、`lmstudio`、`llamacpp`、`vllm`、`litellm` |
| Responses | `openai`、`responses` |
| Anthropic | `anthropic`、`anthropic-compatible`、`claude` |
| Gemini | `gemini`、`google`、`google-generative-ai` |

## 认证与装配

[启动快照](../../internal/providerconfig/snapshot.go)一次解析 provider 的密钥和请求头，并记录解析错误；[工厂](../../internal/providers/factory.go)把已解析的 `APIKey` 和 headers 直接交给适配器。请求期间不再读取 env 或密钥文件，首次使用其他模型也沿用同一快照。主模型和已启用功能的角色依赖必须通过本地配置解析，其他受影响模型选择时返回原因；修改配置或密钥后需重启。

Engine 启动时创建主模型适配器，其余按需创建并复用。`ymz config validate` 不创建适配器、不请求服务，因此不能确认远端认证、模型能力或服务兼容性。

工厂依赖 providerconfig 的已解析配置，各适配器使用 providerapi 消息与协议类型，不依赖 tools。四种协议共用 providerhttp 的路径、请求头、有界读取、网络错误分类和 Retry-After 解析；Responses、Anthropic 和 Gemini 还共用 SSE 读取，Chat 保留自己的 SSE 解析和包含 param 的服务错误诊断，用于 responseFormat 回退。Anthropic 直接查询 modelcatalog 作为输出预算的回退来源。独立图像调用的结果由 [Agent](../../internal/agent/feature_vision.go)转换为工具结果。

## 路径与请求参数

baseURL 必须为绝对 HTTP(S)，不得带 userinfo、查询参数或 fragment。completionPath 是以 `/` 开头的路径，可带查询参数；与基址路径拼接时只消除重复的 `/v1`。Gemini 基址通常只写服务根，自定义路径必须含 `{model}` 和 `:generateContent`；流式调用替换为 `:streamGenerateContent?alt=sse`。

headers 覆盖同名默认头。Anthropic 默认版本头为 `2023-06-01`，可用 anthropicVersion 覆盖。

| 模型设置 | Chat | Responses | Anthropic | Gemini |
| --- | --- | --- | --- | --- |
| temperature | temperature | temperature | temperature | generationConfig.temperature |
| maxTokens | max_tokens | max_output_tokens | max_tokens | generationConfig.maxOutputTokens |
| reasoningEffort | reasoning_effort | reasoning.effort | 不支持 | 不支持 |

配置的 maxTokens 是调用方输出请求的上限，取两者较小值。配置未指定输出上限时，Engine 取目录输出上限、16384、窗口四分之一的最小值；未知输出上限采用 4096 再受窗口约束。达到输出上限会保留已生成文本并报告截断，不能当作完整成功。

Chat 的 responseFormat 只在内部请求带 ResponseSchema 时生效：auto 先用 json_schema，遇到对应不支持错误再回退 json_object。当前主循环、压缩、记忆整理均不传该 schema；它不强制普通回复为 JSON，CLI --json 只控制事件封装。

## 原始状态、压缩与错误

统一消息分别保存正文、可展示思考、工具调用、图片和协议原始回传块。Responses 保留 reasoning 的 encrypted_content 与完整 output；Anthropic 保留 thinking / redacted_thinking / signature；Gemini 保留 thoughtSignature 和完整 parts；Chat 保留兼容服务的 reasoning_content。原始块只回传给原 provider / 协议和实际模型，不能把思考文本当成签名的替代品。

Responses 使用 `store=false`。模型项显式设 `nativeCompaction=true` 时，调用对应 `/responses/compact`，把完整返回窗口作为下一次输入，不裁剪其中的保留项；使用该模型自身压缩。原生调用失败且本次操作未取消时回退通用摘要；用户取消或父 context 到期时直接结束。切换 provider 或模型时仅使用本地抽取文字摘要，可能损失细节；完整原始对话仍在数据库。默认关闭，不能从“兼容 Responses”推断服务支持原生压缩。

仅在尚未向前端交付任何事件时重试可恢复错误，最多额外两次；等待遵循 Retry-After，超过 30 秒则返回错误供用户后续处理。已输出的中断响应保存为部分文本，不重放已执行的工具。流必须收到协议终态才算完成；错误保留有界的服务诊断。

## 图片

`chat.vision.enabled=false` 时不注册图像工具，也不装配独立 vision 模型。开启后，四种协议均可承载图片，具体模型能力由精确元数据或 `supportsImages` 确认。未配置 vision 时，`vision_analyze` 把图片作为当前会话的工具观察交给模型，不另做一次同模型“看图转述”。Chat 通过工具结果之后的用户图片消息承载；Responses / Anthropic / Gemini 使用各自支持的内容块。

显式配置 `models.vision` 时，[图像后端](../../internal/providers/vision.go)调用该模型，再把分析文字返回主会话并报告用量。独立 vision 模型已知不支持图片时，在运行装配阶段报错；未配置独立 vision 且当前模型能力未知或不支持时，在工具调用时返回设置提示，不会静默丢弃图片。

源码：[有界 SSE](../../internal/providers/internal/providerhttp/stream.go)、[原生压缩](../../internal/providers/openairesponses/compact.go)、[重试与工具调度](../../internal/agent/execution.go)。
