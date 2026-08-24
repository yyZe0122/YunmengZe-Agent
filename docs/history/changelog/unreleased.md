# Unreleased (post v0.3.1)

Working notes for the next release. Promote into `docs/history/changelog/vX.Y.Z.md` at publish time ([`docs/release.md`](../../release.md)).

| Area | Change |
| --- | --- |
| TUI | Charm v2 + 焦墨夜/宣纸昼直角色块；居中 `YMZ` landing；header 隐藏 `0.0.0-dev`；空会话不画 context。`GET /v1/sessions/{id}/todos` pills（Ctrl+T）；Ctrl+P/L/S 命令/模型/会话盘。PgUp/PgDn 滚本会话；Ctrl+PgUp/PgDn 切更旧/更新会话；Esc Esc = `/undo`（无独立 chat focus）。tool 卡：process `$ cmd` 拼 `arguments`。full refresh 失败不清空 pills。 |
| IDE | ADR-054：`extensions/vscode` 终端启动器；`make vscode`；发版挂 `ymz-vscode_{version}.vsix`（无 Node 则跳过）。说明：[`docs/wiki/vscode.md`](../../wiki/vscode.md)。不上 Marketplace。 |
