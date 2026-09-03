# YunmengZe Agent 当前状态

更新：2026-09-02（提问/授权卡 + extra-root `/perm`。下一优先 = VS Code V4；O5–O6 / 飞书等用户再提）

**本文件是唯一活着的优化/backlog 文档。** 只写未完成与暂缓项；已落地细节见 ADR（`docs/wiki/adr/`）、[`docs/wiki/database.md`](../wiki/database.md)、changelog 与 git。目录：[`docs/README.md`](../README.md)。

## 现状

生产形态稳定：`ymzd` + CLI·TUI（`ymz`）+ `core.db`。设计知识库：`docs/wiki/`。  
当前发布线：**v0.5.0** = models.dev 填窗 + Phase S/W/M。v0.4.0 = 清宣纸 TUI（ADR-053）+ VS Code 启动器（ADR-054）+ `/edit` retract + `fs_remove`。v0.3.1 = harness + Tab Auto。

| 对标 | 契约重叠（粗） | 说明 |
| --- | --- | --- |
| OpenCode 配置/协议 | ~80–90% | + `import-opencode`；stdio + 远程 MCP（O2） |
| OpenCode 产品手感 | ~85–90% | + Phase Q 编码循环（ContextView / fs 行窗+hash / `process_shell` / session todo / rewind） |
| OpenCode API/SDK | ~10–15% 路径类比 | 本地 `/v1/*` + Go `gatewayclient`；**不追**全量 OC OpenAPI |
| Crush TUI | 契约 ~95%；画面不对标 | 快捷键/perm/steer/折叠仍学 Crush 契约；**chrome 为清宣纸**（不抄 Crush 圆角/渐变/字母表） |
| Hermes 分层记忆 | 架构 ~90% | + H6；**H1-lite curator**；**H5-lite** `default_ttl` + 过期软归档（冻结块仍手动 refresh） |
| Hermes 自进化 | ~40% | H3 草稿+人工 apply；H4 习惯提示；H5-skill 软归档（ADR-050）；无自动 apply / yolo |
| Hermes 委派 | ~50% | 同步 `task` + `kind` general/explore/web + 叶子禁带；**无** 并行 / 后台 / orchestrator |
| Hermes Media & Web | ~70% | `web_search`/`web_extract`/`http_get` + 辅助 vision/speech/video 工具；无实时 Voice / 图生 |
| Hermes 消息网关 | ~0% | 仅本机 UDS/loopback；**暂不上**飞书/微信（本机编码/定时为主） |

**产品焦点：** 本机编码循环质量（工具观察 + turn/step/inbox + TUI 跟手）+ 简单任务 + 定时任务。通道/SDK 是添头。  
**下一优先：** **VS Code V4**（Claude Code 式侧栏）。O5–O6 / H2 / 飞书 M* / Marketplace 仍用户再提。

## 原则（不变）

- 三件套：daemon + CLI·TUI + `core.db`。不恢复 Module Runtime、多 DB、交互 **Planner**（plan-step 整单审批轨）。
- 工具副作用只经 Tool Broker；Policy → Approval → Capability Grant → containment → 限流 → Audit。
- Skill 仅指令文本，不扩大授权；`skill_ids` 仅显式预载（TUI/job）；Prefix 注入技能目录（id+一句话）；正文仍 `skills_list` → `skill_view`（ADR-036 / 052）。用户规则：`<ConfigDir>/AGENTS.md` + 可选项目 `.yunmengze/AGENTS.md`（子代理同样继承）。
- plan 永远只读；高风险工具仅 agent。TUI Tab **Agent → Plan → Auto**：Agent 未预授则 `/perm`；Auto 为本 session 预授 process+git（切走结束）。记住放行：`chat.permission.allow` 或 `chat.tools.*`（OR）。cron / CLI 永不 wait。见 ADR-038 / 043 / 046。
- 会话记忆为 in-process MemoryManager（ADR-044），非独立 Memory 进程。
- **客户端分层（ADR-018/022/054）：** 业务用例只在 daemon；Gateway 仅 HTTP 适配；CLI 与 TUI 经 `gatewayclient` 并列，TUI **不** exec CLI、**不** import tools/providers/agent。VS Code 扩展（本相）只启动 TUI，不走 Gateway HTTP。拖文件 / 原生聊天 = V4 Webview，终端启动器做不到。
- **子代理：** 逻辑子 Run（`parent_run_id` + `task` 工具，ADR-039）；同步阻塞；grant/工具不得扩大。`task.kind` 目录：广告 `general`/`explore`/`web`；配了 `models.vision|speech` 才广告 vision/speech/video。子永远叶子。**不上** Hermes 并行/后台/`role=orchestrator`。
- **消息通道（规划）：** 第二客户端 → `tasksubmission` / `taskcontrol`；**不**在 Gateway 内跑 tool/provider/grant。
- **Go 精神：** 具体类型 + 调用方小接口；composition root 在 `cmd/`；无 DI 容器 / ORM / 通用事件总线。

---

## 已关闭（摘要）

细节在 changelog 与 ADR，不在本文件展开。

| 轨 | 内容 | 发版 |
| --- | --- | --- |
| T / C / UX | T1–T8 · C1–C4 · UX-A/B（气泡 TUI + 节流 live MD） | v0.2.4–v0.2.5 |
| TUI 视觉 | 泽夜/泽昼 · 去套娃 · 平铺消息 · slash 芦金；会话仅 `/sessions` overlay | v0.2.6 |
| T8 收紧 | 冻结安全前缀 glamour；trail 永远 plain；32ms 合帧；`streamingMD` 挂 model | v0.2.7 |
| O1–O4 | import-opencode · MCP remote · `chat.commands` · session prefer | v0.2.4 |
| H* 已落地 | H7 pin · H1-lite · H5-lite · H3 draft · H4 hint · H5-skill · H6 injectscan | v0.2.4–v0.2.5 |
| R | 改名 YunmengZe / `ymz` / `~/.yunmengze` | v0.2.0–v0.2.1 |
| L1 · D1 | 结构化日志 · GoReleaser tap/scoop | v0.2.x |
| **Q** | QA–QH 编码循环：ContextView · 文件工具 · `process_shell` · session todo · L3 tool 索引 · rewind · TUI Esc/`/undo` | **v0.2.8**（ADR-051 · migration 026） |
| **Q-harden** | 单 packer（热路径仅 L1）· through 滑窗保 tail · 真 model id · todo 留 Ephemeral · HistoryBudget≤usable · 短编码 prompt | **v0.2.8** |
| **TUI leave** | `/new` 离焦 ready + cancel 本轮；无焦点丢 stream/refresh/perm SSE；去 bubblezone 划选复制；窄屏不 inflate | **v0.3.0** |
| **Tab stance** | Tab Plan→Agent→Auto；内核仍 agent\|plan；交互 Agent `/perm`；Auto = session 预授 process+git；`permission.allow` OR `tools.*` | **v0.3.1** |
| **R** | 观察合同 · turn/step/inbox · steer · `ask_user` · Prefix 技能目录 · agent `http_get`（plan/cron 不广告） | **v0.3.1**（ADR-052 · migration 027） |
| **README** | 产品向 README + `README.zh.md`；删过时架构 SVG | **v0.3.1** |
| **TUI-ink** | 清宣纸 chrome + 盲文毛笔 landing（ADR-053） | **v0.4.0** |
| **IDE-launcher** | VS Code 终端启动器 V0–V3（ADR-054） | **v0.4.0** |
| **QG retract** | `/edit` · `/editundo`；`fs_remove`；`edit_revisions.kind`（028） | **v0.4.0** |
| **TUI cards** | 提问/授权真卡片；自定义回答；Esc Esc dismiss；extra-root 四档 | **unreleased** |

同包大文件拆分已落地（`tui/cmds_*`+`update_*`、`kernel/repository_*`、`tools/fs_*`、`cmd/ymzd/wire_*`）。再拆触发：新 slash / 新聚合 SQL / `ymzd` 接线难 review → 同包再拆。

---

## 有序 backlog

依赖顺序：

```text
Phase 1：O1–O4 ✅ ──► O5–O6（用户再提）
Phase 2：C1–C4 + UX-A/B ✅ · T8 live MD ✅
Phase 3：H* 已落地 ✅（除 H2）
Phase Q：编码循环 QA–QH + Q-harden ✅（v0.2.8；细节 ADR-051）
Phase R：harness 循环语义（ADR-052）✅
Phase TUI-v2：Charm v2 机械迁（引擎/textarea/快捷键/todos）✅ v0.4.0
Phase TUI-ink：清宣纸 chrome ✅ v0.4.0
Phase QG retract：`/edit` · `fs_remove` · kind 028 ✅ v0.4.0
Phase IDE-launcher：VS Code 终端启动器（ADR-054）V0–V3 ✅ v0.4.0
Phase S：类型化子代理 `general`/`explore` ✅
Phase W：网页检索 `web_search`/`web_extract` + `kind=web` ✅
Phase M：辅助媒体 vision/speech/video ✅
Phase IDE-webview：VS Code V4   ← 下一优先
H2 / O5–O6 / 飞书 M* / Marketplace ── 用户再提（不插队）
```

### Phase S — 类型化子代理 ✅

已落地：kind 目录 `internal/tools/taskkind.go`；广告 `general`/`explore`/`web`；子永远剔 `task`；显式禁带请求 fail-closed；Prefix 不再写死「No vision」。`vision`/`speech`/`video` 在目录里占位且 `Advertised=false`。`AllowedModelRoles`：`subagent`/`compact`/`web`/`vision`/`speech`。

```json
{ "prompt": "…", "kind": "explore", "tools": ["fs_grep"] }
```

| ID | 项 | 状态 |
| --- | --- | --- |
| **S0** | 扩 ADR-039 / ADR-045 | ✅ |
| **S1** | `task` kind + 叶子 | ✅ |
| **S2** | `AllowedModelRoles` 报错数据源 | ✅（W 已加 `web`） |
| **S3** | kind 过滤 / 禁带 / 未广告不建 Run | ✅ |

**不做（本轨）：** Hermes `delegate_task` 并行/后台、`role=orchestrator`、worktree isolation、`/review`、`/agents` overlay。

### Phase W — 网页检索 ✅

已落地：`web_search` / `web_extract`（Broker）；交互 agent 广告，plan/cron 不广告；`/perm similar` = host。`chat.web.search` = `ddg`（默认）| `searxng` | `tavily`；缺 URL/key → LoadChat 失败。SearXNG 配置 URL 的 host 仅对 `web_search` 作 SSRF 例外。`task(kind=web)` 已广告；`models.web` 进 whitelist（缺则回落 subagent）。父集没有 web 工具则不建 Run。

**不做：** Playwright / `browser_*` / Computer Use。

### Phase M — 图片 / 语音 / 视频 ✅

已落地（ADR-055）：`vision_analyze`（path 或 `/perm` url）、`audio_transcribe`（Whisper `/v1/audio/transcriptions`）、`video_analyze`（ffmpeg 抽 ≤8 帧）。未配 `models.vision|speech` 不广告。主循环仍文本。`task(kind=vision|speech|video)` 配了 role 才广告。

**不做：** `audio_speak` / `models.tts`、`image_generate` / `video_generate`、实时麦克风、TUI 贴图。

配置示例（改 `models.*` 需 `ymz restart`）：

```json
{
  "model": "deepseek/deepseek-chat",
  "models": {
    "subagent": "deepseek/deepseek-flash",
    "compact": "deepseek/deepseek-flash",
    "vision": "openai/gpt-4o-mini",
    "speech": "openai/whisper-1",
    "web": "deepseek/deepseek-flash"
  }
}
```

无新表则 **不必** 新 migration（子 Run 仍用 `parent_run_id`）。

### Phase IDE-launcher / V4（S/W/M 之后）

V0–V3 已发 **v0.4.0**：`extensions/vscode` 启动 TUI；`make vscode` 本机打包；发版脚本 / Actions 在 goreleaser 之后挂 `ymz-vscode_{version}.vsix`。用户说明：[`docs/wiki/vscode.md`](../wiki/vscode.md)。架构：[ADR-054](../wiki/adr/054-vscode-terminal-launcher.md)。

终端 TUI **接不住**拖文件。要 Claude Code 式 GUI 就开 **V4 Webview**（新 ADR 或扩 ADR-054）：侧栏 → `gatewayclient` HTTP/SSE，`interactive: true`；拖文件 / 资源管理器 → 工作区相对路径写入用户消息（`@path`）；**不**在扩展里跑 tool。关编辑器仍不 `ymz stop`。不上 Marketplace。

启动器薄补丁（cwd = 工作区、设置里填 `ymz` 路径）可当 V4 前的小步，不挡 S/W/M。

| ID | 项 | 状态 |
| --- | --- | --- |
| **V0** | ADR-054 + 脚手架 | ✅ |
| **V1** | 三命令 + PATH + `@file#L` | ✅ |
| **V2** | `make vscode` 本机 VSIX | ✅ |
| **V3** | Release 挂 VSIX | ✅ |
| **V4** | 原生侧栏 Webview 聊天 + 拖文件 | 下一优先 |
| **V5** | Marketplace / Open VSX | 用户量上来再做 |

### 等用户再提

| ID | 项 | 目标 | 约束 / 验收 |
| --- | --- | --- | --- |
| **O5** | 薄 compat 子集（可选） | `/compat/opencode/*` 映射 session/message/compact/skills/health | 文档 **compat profile v0.1**；PTY/LSP → 501；**非** SDK drop-in |
| **O6** | 最小 OpenAPI + 可选 TS 客户端（可选） | 只覆盖真路径或 O5 | 不宣称全量 OC SDK；IDE Webview 若落地再触发最小契约 |
| **H2** | write_approval | messaging/cron 来源的 memory/skill 写入先 stage | 跟飞书 M* |
| **V5** | Marketplace / Open VSX | publisher + `vsce publish` | 用户量上来再做 |

### Phase 4 — 消息通道（飞书 / 企微 / 微信）

形状：`internal/channel`（transport + 身份 + 限流）→ `tasksubmission` → chatsession → Broker → adapter 回消息。本地 Gateway 边界不变。

| ID | 项 | 目标 | 约束 / 验收 |
| --- | --- | --- | --- |
| **M0** | Channel Bridge ADR | 架构 + 安全默认（落地时新编号，不预占 049） | 默认 deny；session 映射在 `core.db` |
| **M1** | **飞书**（第一通道） | 优先出站 WebSocket（无需公网） | @提及 + open_id allowlist |
| **M2** | 企业微信 | 官方 bot WS / callback | 企业场景 |
| **M3** | 个人微信（可选） | iLink long-poll 类 | **DM 为主**；群弱；ToS/产品风险高 |
| **M4** | Pairing + allowlist | 与 M1 同发 | TUI/CLI 批准配对码；空名单=拒绝 |
| **M5** | 通道权限档 | 新远端默认 plan 或 ask | agent 写需显式 promote |
| **M6** | Cron 跨端投递 | job `deliver: feishu:chat_id` | delivery 仅 adapter；内容来自 completed run |
| **M7** | composition 接线 | `cmd/ymzd` 挂 adapter | Gateway **不**成为 tool 宿主 |

**安全默认：** 通道文本不可信；无 pairing 不跑；cron/messaging 高风险 fail-closed。

---

## 可选（非阻塞，未承诺）

| 项 | 说明 |
| --- | --- |
| **gatewayclient 薄 ops** | CLI↔TUI 组请求胶水痛时再抽；不新建第二控制面 |
| CLI skills | `run --skill` / `skills list`（主路径仍是 TUI `/skills`） |
| CJK FTS 扩展 | unicode61/trigram + LIKE 兜底；专用 C 扩展另议 |
| `audio_speak` / `models.tts` | Phase M 可后做；不挡 transcribe |
| **O5–O6** | 仅有真实外部 OC 客户端绑定时（用户再提） |

```text
用例（daemon services）     → 已统一
外观（gatewayclient）       → 已共享
语法（CLI argv / TUI slash / HTTP）→ 故意分叉
IDE（extensions/vscode）    → 本相只启动 TUI；V4 Webview 在 S/W/M 之后才走 HTTP
通道（channel adapter）     → 规划中；与 gatewayclient 并列的 Core 客户端
```

## 暂缓 / 不做

| 项 | 说明 |
| --- | --- |
| Hermes 并行/后台 `delegate_task` / orchestrator | 本轮保持同步扁平 `task`；并行另案 |
| Playwright / 真浏览器 / Computer Use | 网页 = search + extract + `http_get` |
| 主循环多模态 / TUI 剪贴板贴图 | 媒体走辅助工具回文本；`/paste` 另案 |
| 实时 Voice Mode / Discord 语音房 | 语音 = 文件转写（+ 可选 TTS 落盘） |
| `image_generate` / `video_generate` | 厂商图生另案 |
| 全量 OpenCode API/SDK 兼容 | ~162 路径 + 生成 SDK；与三件套冲突；只用 O5 子集或体验兼容 |
| Crush 鼠标抓取 | 不设 `MouseMode`；终端原生划词复制 |
| streaming 每 token 全量 glamour | 禁止；T8 = 合帧 + 冻结前缀 glamour + trail 永远 plain + 未闭合围栏不切 + 失败回退 C2 |
| Crush 三档 permission | 保持 once/similar/permanent/deny 四档 |
| 沙箱 phase-2+ | namespace / bubblewrap / seccomp |
| LSP | 另案 |
| Provider 费用真值 | 以后台账单为准 |
| Cron 表达式 | 固定 interval 已够（除非 M/cron 强需求再议） |
| monorepo 全量 client DTO | alias 可接受 |
| TUI 与 tools 同进程 | 禁止 |
| yolo / per-tool 软权限 | 禁止 |
| 独立 Evolution / 多 DB / Module Runtime | 禁止 |
| 云端向量记忆默认 / Mem0 全家桶 | 禁止作默认路径 |
| 改名后旧路径/旧 CLI 长期兼容 | **不做** |
| winget / npm | **不做** |

## 不建议引入

通用模块框架、Actor/MQ、ORM、DI 容器、工作流 DSL、跨 CLI/TUI/Gateway 的 Command Bus、恢复独立 Planner 审批轨、TUI 开 DB/exec CLI、本地 token→$ 定价表、未达标即宣称「OS sandbox」、跨会话云端向量记忆、多业务 SQLite、Gateway 内执行 tool/provider。

## 常用命令

```bash
make check
make build
make all          # check + build + ymzd --check
make vscode       # optional: package extensions/vscode → .vsix（需 Node）
go test ./... -count=1
```
