# Unreleased (post v0.5.0)

Working notes for the next release. Promote into `docs/history/changelog/vX.Y.Z.md` at publish time ([`docs/release.md`](../../release.md)).

| Area | Change |
| --- | --- |
| TUI | Question/permission **cards**: full prompt, numbered options, `Type your own answer`, multi-question ←→ wizard, multi-select Space. Esc Esc (3s) dismisses. Cards no longer steal Enter as steer. |
| ask_user | Custom answers allowed when options exist. `POST /v1/questions/{id}/dismiss`. Wait 30m. |
| /perm | Extra-root for `fs_*` / process / git absolute paths: once (this call, `AddOnceRoot`) · similar (session `extra_roots`) · permanent (`agent.local.json` `chat.workspace.allow`, process `AddRoot`) · deny. Wait 30m. CLI/cron deny. Volume root `/` rejected. |
| Config | Default `EnsureConfig` / README seed includes `models.subagent` and `models.compact` (same ref as `model`). |
| Prompt | Interactive TUI: call tools and wait for `/perm`; do not tell the user to edit `agent.json`. |
| Sub-agent | Explicit `task_id` resume (same child `run_id`); parent packing/TUI/`session_search` exclude child traces. Resume does not reverse `runs.state`; orphan `running` (no in-process runner) can be taken over. `ResumePrompt` appends after incomplete history. Pre-029 children (empty `child_kind`) cannot resume. |
