# Unreleased (post v0.3.1)

Working notes for the next release. Promote into `docs/history/changelog/vX.Y.Z.md` at publish time ([`docs/release.md`](../../release.md)).

| Area | Change |
| --- | --- |
| TUI | Charm v2 + 清宣纸 chrome（少框、无 `░▒`）。盲文毛笔仅 landing；紧凑 header；status 在输入框下；IME 硬件光标；slash 补全盖对话底。`GET /v1/sessions/{id}/todos` pills（Ctrl+T）；Ctrl+P/L/S 盘。 |
| TUI | Tab 循环 agent→plan→auto。空闲回车提交新一轮；steer 仅 running turn；409 回退 submit。 |
| IDE | ADR-054：`extensions/vscode` 终端启动器；`make vscode`；发版挂 `ymz-vscode_{version}.vsix`（无 Node 则跳过）。说明：[`docs/wiki/vscode.md`](../../wiki/vscode.md)。不上 Marketplace。 |
