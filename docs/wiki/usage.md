# CLI 与 TUI

源码：[CLI](../../cmd/ymz/main.go)、[TUI 按键](../../internal/tui/tui.go)、[TUI 命令](../../internal/tui/commands.go)。

## CLI

| 命令 | 用途 |
| --- | --- |
| `ymz` / `ymz tui` | 打开终端界面 |
| `ymz run [参数] "任务"` | 执行一次用户轮次后退出 |
| `ymz sessions` | JSON 列出启动目录的主会话，最近优先，最多 200 条 |
| `ymz models [refresh]` | JSON 列出主模型、角色模型及 provider 目录模型，去重后按 ID 排序；refresh 先刷新模型元数据缓存 |
| `ymz config validate` | 补齐缺失模板，校验主模型与已启用功能依赖，报告其他模型不可用原因；不打开数据库或请求远端 |
| `ymz db check` | 打开数据库、应用迁移，再执行 SQLite quick_check |
| `ymz paths` | JSON 输出 config、database、workspace 路径 |
| `ymz version` | 版本、提交、构建日期 |
| `ymz help` / `ymz --help` / `ymz -h` | 帮助 |

`sessions` 也会应用数据库迁移；它与 `db check` 使用短生命周期数据库连接，不依赖模型凭证。`config validate` 不验证远端凭证、模型能力或逐个实例化适配器；主模型和已启用功能依赖通过时返回成功，其他不可用模型的原因写入 stderr。

以下参数供默认 TUI、tui 和 run 使用，必须放在任务文本前；`--session` 与 `--continue` 互斥。

| 参数 | 行为 |
| --- | --- |
| `--mode agent` / `--mode plan` | 新会话默认 agent；续接未显式指定时保留保存值 |
| `--model provider/model` | 新会话默认配置的 model；续接未指定时保留保存值 |
| `--session ID` | 续接指定主会话，使用其保存的工作目录 |
| `--continue` | 续接启动目录最近的主会话，没有则新建 |
| `--json` | run 输出 JSON Lines；不改变 TUI 显示 |

`--model` 只选择会话模型，不跳过全局启动快照检查；配置的主模型和已启用功能依赖仍须满足[启动条件](configuration.md)。

```sh
ymz --mode plan
ymz run --continue "继续分析配置加载过程"
ymz run --json "更新 README 的安装步骤"
```

run 的任务可使用技能或模板，如 `ymz run "/review 当前修改"`，不接受 TUI 内置管理命令。普通输出的正文在 stdout，工具开始提示、运行诊断（包括技能跳过原因）和错误在 stderr；失败返回非零退出码，Ctrl+C 取消，已保存消息保留。

## TUI 操作

| 按键 | 行为 |
| --- | --- |
| Enter / Shift+Enter、Ctrl+J | 发送 / 换行 |
| Esc | 取消当前轮次；空闲时清空输入；选择器中关闭 |
| Ctrl+C | 运行中取消，空闲时退出 |
| Tab、Shift+Tab | 空闲时切换 plan / agent |
| Ctrl+L / Ctrl+S | 空闲时打开模型 / 会话选择器 |
| PgUp、PgDn / Ctrl+U、Ctrl+D | 整页 / 半页滚动 |
| `e` / `E` / `c`（输入为空） | 切换最近块 / 展开全部 / 折叠全部工具与思考 |

模型选择器标出启动时不可用的模型及原因；包括准备新会话时的选择在内，选择不可用模型会立即报错并保留原选择。技能 / 记忆禁用后不出现在帮助和补全中；手动输入对应管理命令会提示禁用。

选择器和 slash 补全用 ↑↓、Enter、Esc。运行中发送的消息进入最多 32 条的纠偏队列，在下次模型循环处理，不打断当前工具；结束时未消费的内容回填输入框。

| 命令 | 行为 |
| --- | --- |
| `/new` | 准备新会话，保留模式与模型；运行中先取消 |
| `/sessions [ID]`、`/resume [ID]` | 选择或加载主会话 |
| `/model [provider/model]` | 选择或设置模型 |
| `/mode plan`、`/mode agent` | 设置模式 |
| `/skills`、`/<技能 ID> [参数]`、`/<模板名> [参数]` | [发现技能或展开消息](skills.md) |
| `/compact [重点]` | 手动压缩较早的对话，Esc 可取消 |
| `/memory` | [查看、纠正或遗忘偏好及证据](memory.md) |
| `/memory edit <id> <偏好>`、`/memory forget <id>` | 纠正或遗忘记录，仅 agent |
| `/status` | 显示模式、模型、会话 ID、工作目录 |
| `/help` | 帮助 |
| `/quit`、`/q`、`/exit` | 取消当前运行并退出 |

运行中只允许内置的 `/help`、`/new`、退出命令；技能与模板可作为纠偏排队。新会话首次发送时才保存，`/new` 不删旧会话；模型与模式保存到会话，不改全局配置。子会话不在选择器中，由父会话使用 task_id 续接。

`ask_user` 只在 TUI 可用：↑↓ 选项，多选时空输入可按 Space 勾选，Enter 提交；手动文本优先，Esc 取消本轮；多个子任务问题排队。子任务进度独立显示，不混入主回复；文件编辑直接显示有界差异预览。运行诊断追加到 `<config>/ymz.log`，启动准备错误可能写入 stderr。

## JSON 事件

| type | 字段与含义 |
| --- | --- |
| `session` | `id`：本次会话 |
| `delta`、`thinking`、`info` | `text`：正文、思考或运行信息 |
| `usage` | `usage`：token 与可选缓存用量；辅助调用另有 `purpose`（compact、memory、vision_analyze） |
| `assistant_end` | 当前 assistant 响应已保存，不表示任务结束 |
| `tool_start`、`tool_progress`、`tool_end` | `tool` 为工具名，`text` 为 JSON 参数或结果 / 错误观察 |
| `task_start`、`task_end` | `task_id`、`text`：子任务开始与结果状态 |
| `done` | `session_id`；失败时附加 `error` |

子任务事件带 `task_id`；CLI 普通正文只输出主回复，JSON 保留全部事件。TUI 汇总本轮含辅助调用的 token，金额仅在服务提供时可用。TUI 另使用 `steer` 事件接收纠偏消息。准备阶段出错可能没有 session / done；不同协议的增量行为见[Provider 协议](provider-protocols.md)。

运行期间 stdout 或诊断输出写入失败会取消本轮，沿用工具观察保存和租约释放流程，最终返回首次写入错误。输出通道失效时不能保证交付 done 事件。
