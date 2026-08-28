# ADR-055：辅助媒体边界

- 状态：Accepted
- 日期：2026-08-28

## 背景

主循环是文本 tool loop（`Message.Content string`）。产品需要看图 / 转写 / 抽帧，但不能把热路径改成多模态，也不能上浏览器或实时 Voice Mode。

## 决策

辅助媒体走 **Tool Broker 内一次调用**，正文回主模型。

| 工具 | 触发 | 后端 |
| --- | --- | --- |
| `vision_analyze` | workspace `path` 或 `/perm` 后的 `url` + `prompt` | `models.vision` 一次 `Complete` + `Images []ImagePart` |
| `audio_transcribe` | workspace 音/视频 `path` | `models.speech`：`POST /v1/audio/transcriptions` multipart（**不是** Complete） |
| `video_analyze` | workspace 视频 `path` + `prompt` | `ffmpeg` 抽 ≤8 帧 → 同 vision Complete；无 ffmpeg → 观察错误 |

未配置对应 `models.*` → **不注册、不广告、plan 不写 capability**。`models.video` 永不单列（复用 vision）。`audio_speak` / TTS 本轮不做。

### Images 合同

`CompletionRequest.Images` 可选。只在 openai-chat / anthropic / gemini 序列化；openai-responses 带 Images → 明确错误。主 tool loop **永不**填 Images。厂商只收本机字节（data/base64），不把 URL 交给厂商。

### 授权

- path：PathGuard + 预授 workspace 只读。
- `vision_analyze` url：与 `http_get` 同闸（once+session 不预发；similar = host；SSRF 基线；plan/cron 无 url）。
- plan 可广告 path 类媒体工具（只读）。

### 接线

`internal/tools` 只吃 `VisionCompleter` / `AudioTranscriber`。composition root（`cmd/ymzd`）从 `LoadModelRoles` 解析 vision/speech，注入 Broker。`BuildRoleEndpoints` 跳过 `speech`。`task.kind` vision/speech/video 在对应 role 已配置时广告；speech 子循环 Role=`subagent`；未配 fail-closed 不建 Run。路径与抽帧输入 cap 16MiB。

### 不做

主循环多模态、TUI `/paste`、图生/视频生、实时麦克风、Playwright、把 Whisper 伪装成 Complete。

## 后果

- 文本主模型可委派看图/听写/抽帧而不改 packing。
- 改 `models.vision|speech` 需 `ymz restart`。
- ffmpeg 缺失不崩 turn。
