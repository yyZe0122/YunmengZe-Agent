# ADR-008：初始威胁模型

- 状态：Accepted
- 日期：2026-07-13
- 更新：2026-07-30
- 更新：2026-08-03（双轨 chat / chat-native Job）
- 更新：2026-08-10（session workspace / permission tiers，ADR-046）

## 保护目标

保护主机文件、凭据、外部账户、系统服务、Agent Policy、审批与 Grant 记录、以及 `core.db` 中的任务/审计事实，防止模型输出、外部内容或本地客户端扩大权限。

## 当前威胁

- Prompt injection 导致工具滥用或越权路径/命令；
- Grant 绑定 Plan Hash 后参数被替换（须 Hash + Grant 匹配）；
- 路径穿越和符号链接/junction 逃逸；
- Session chat 预授权过宽（plan 只读；agent 写受 `chat.allow_write`；git/process 默认关，见 ADR-038）；
- `chat.workspace.allow_all` 或过宽 `allow`/唤起目录（默认 session 根 = client cwd，ADR-046）；
- `allow_permanent` 信任表（ConfigDir `permissions-trust.json`）被误批后跨会话复用；
- 无人值守 cron agent（ADR-042）与交互 agent 同权，误配 roots/`chat.tools` 扩大面；Job 路径不 wait permission（fail closed）；
- 本地 Gateway 被同机其它用户滥用（socket 权限 / Windows token；任意本地客户端可 decide permission）；
- Scheduler 或 Gateway 试图绕过 Broker（Job 只提交 chat task，不执行 tool）；
- 日志泄漏 Secret；
- SSRF（HTTP 工具域名审批 ≠ 完整 SSRF 防护；`http_get` / `web_extract` / `web_search` / `vision_analyze` url 进 agent 广告但不预发，须 `/perm` 后按 host 收窄；plan/cron 无 url。唯一例外：`chat.web.searxng_url` 的 host 仅放行 `web_search`）。

## 历史威胁（已删除架构）

下列项针对已移除的进程外模块/Evolution，保留作反回归提醒，**不是**当前产品面：

- 模块进程伪造身份、跨 DB 越界、Evolution 改写核心安全代码。

## 初始控制

结构化 Plan、Plan Hash、Capability Grant、类型化工具、路径规范化、事件幂等、Secret 脱敏、默认拒绝策略、Gateway 不执行 tool/不发 grant。更强 Linux sandbox 见 `docs/wiki/security/linux-sandbox-roadmap.md`。
