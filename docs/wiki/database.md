# 数据库

`<config>/core.db` 是唯一业务数据库，使用纯 Go 的 modernc.org/sqlite。Engine 与会话、记忆共用一个 handle，最多一个连接；开启 WAL、foreign_keys、5000 毫秒 busy_timeout 和 synchronous=FULL。源码：[SQLite](../../internal/store/sqlite/db.go)。

## 当前数据结构

| 表 | 保存内容 |
| --- | --- |
| `schema_migrations` | 已执行迁移的 version、applied_at |
| `chat_sessions` | 会话 ID、父 ID、kind、标题、工作目录、模式、模型、文字摘要、完整原生压缩窗口及锚点、租约和时间 |
| `chat_messages` | 自增消息 ID、会话外键、message / usage JSON、时间 |
| `memory_entries` | 偏好 / 旧记录 ID、作用域、正文、来源、证据 JSON、稳定 key、kind、优先级、到期和归档时间 |
| `memory_curation_state` | 全局整理游标、最后尝试时间，跨进程原子领取批次 |
| `memory_entries_fts` | entry_id、content 的 FTS5 检索索引；SQLite 自行维护影子表 |

会话结构见 [030](../../migrations/core/030_local_agent.sql)，记忆结构见 [019](../../migrations/core/019_memory_entries.sql)、[020](../../migrations/core/020_memory_productization.sql)、[022](../../migrations/core/022_memory_archive.sql)。[031](../../migrations/core/031_user_preferences.sql)增加偏好证据、唯一 key 和整理游标，[032](../../migrations/core/032_native_compaction.sql)增加 summary_replay。mode 只接受 agent / plan，消息 JSON 有有效性检查；parent_id 与记忆 session_id 没有数据库外键。新写入时间为 UTC RFC3339Nano。

## 会话与记忆

[会话 Store](../../internal/session/store.go)按消息 ID 保存完整对话，追加消息与更新会话时间在同一事务内。首条用户消息前 80 字符用作空标题；主会话列表按启动目录筛选、最近优先，最多 200 条，隐藏子会话。

摘要只更新 summary / summary_through / summary_replay，原消息保留；TUI 恢复完整消息，模型使用摘要及锚点后消息。消息 JSON 可保存图片和协议原始回传块；原始用户文字与展开后的技能内容分开，避免技能进入偏好证据。

[偏好写入与纠正](../../internal/memory/preferences.go)把条目与 FTS 更新放在同一事务；[记忆 Store](../../internal/memory/store.go)负责查询等存储操作。空 session_id 表示全局；查询排除过期 / 归档条目；偏好自然过期仅由 expires_at 判断，主动遗忘写入 archived_at。旧归档不自动恢复，非偏好旧记录仍可到期软归档。自动注入仅取 preference；旧记录仍可查询，遗忘的 key 保留以阻止同 key 自动恢复。

运行租约有效期 2 分钟，主循环和手动压缩共用租约生命周期，每 30 秒续期，防止同一会话并发运行或修改模式 / 模型。正常结束或取消时先停止并等待续期，再释放租约；续期失败取消当前操作。崩溃后等待到期。

## 迁移与备份

打开数据库时按文件名顺序执行未登记的内嵌 SQL，每个文件独立事务，版本记录和脚本一同提交或回滚。已发布迁移 001–029 不修改，结构变更追加有序迁移；当前结构以全部迁移执行结果为准。

事务外检查用于跳过已完成迁移。待执行迁移在事务中先尝试唯一版本登记，以取得 SQLite 写锁并复查：若另一进程已经提交该版本，则跳过脚本；否则执行脚本后一起提交。失败回滚会同时撤销本次版本登记。锁等待沿用 5000 毫秒 busy_timeout，超时仍会返回错误；此流程不依赖打开数据库后才取得的会话租约。

`ymz`、`ymz run`、`ymz sessions`、`ymz db check` 都可能应用迁移；db check 随后执行 quick_check，并非只读打开。config validate 不打开数据库。

关闭记忆或其他可选能力不会删除表、记录或迁移，也不跳过共享数据库的迁移流程；功能关闭只影响对应能力的装配与运行。

备份或恢复前退出所有使用该配置目录的进程，再复制整个目录，包括存在的 core.db-wal / core.db-shm、配置、规则和技能。没有内置备份 / 恢复命令；恢复时使用与备份结构相容的程序版本。
