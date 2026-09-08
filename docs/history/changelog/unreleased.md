# Unreleased (post v0.7.0)

Working notes for the next release. Promote into `docs/history/changelog/vX.Y.Z.md` at publish time ([`docs/release.md`](../../release.md)).

| Area | Change |
| --- | --- |
| VS Code | Tab / Shift+Tab 循环 agent→plan→auto；权限卡浮层 + 2s poll；`/perm` 可带 decision；Esc Esc deny |
| VS Code | 时间线工具摘要 / orphan·interrupted 错误条；composer AGENT/PLAN/AUTO 印 |
| VS Code | GUI 静默 `ymz start`（`windowsHide`，不开终端）；去掉 `ymz.useTerminal` |
| VS Code | 独立 TUI VSIX `ymz-vscode-tui`；两边会话 cwd = 当前文件夹；发版两包必挂 |
| Agent | 取消时 in-flight + 同 step 未执行 sibling 写入 `interrupted` 观察，packing 不再补 `orphan_tool_call` |
