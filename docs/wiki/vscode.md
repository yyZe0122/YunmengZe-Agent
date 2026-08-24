# VS Code / Cursor 扩展

用户安装与发版挂包。架构边界见 [ADR-054](adr/054-vscode-terminal-launcher.md)。活尾巴（原生聊天 / Marketplace）只在 [`docs/backlog/current.md`](../backlog/current.md)。

源码：[`extensions/vscode/`](../../extensions/vscode/)。扩展 **不** 进 `go.mod`；`make check` 不跑 npm。

## 它做什么

在集成终端里打开已经安装的 **`ymz` TUI**（分屏）。不是聊天侧栏，不上 Marketplace。

| 命令 | 快捷键 | 行为 |
| --- | --- | --- |
| YunmengZe: Open TUI | Ctrl/Cmd+Esc | 已有名为 `ymz` 的终端则 focus，否则分屏新建 |
| YunmengZe: Open TUI in new tab | Ctrl/Cmd+Shift+Esc | 始终新开一列（编辑器标题栏同一按钮） |
| YunmengZe: Insert @-file reference | Ctrl+Alt+K / Cmd+Alt+K | 插入 `@path` / `@path#L12` / `@path#L12-20`（不回车） |

需要本机已有 `ymz`（`PATH` 或 `~/.local/bin/ymz`）。找不到会报错，不会在终端里打出 command not found。关编辑器 **不停** daemon（`ymz stop`）。

## 安装

**从 GitHub Release**（发版后；资产名 `ymz-vscode_{version}.vsix`，`{version}` 无前导 `v`）：

```bash
# example tag v0.4.0 → ymz-vscode_0.4.0.vsix
curl -fsSL -o ymz-vscode.vsix \
  "https://github.com/yyZe0122/YunmengZe-Agent/releases/download/v0.4.0/ymz-vscode_0.4.0.vsix"
code --install-extension ymz-vscode.vsix
```

Cursor / VSCodium：命令面板 → **Extensions: Install from VSIX…**

**从源码（本机）：**

```bash
make vscode
code --install-extension extensions/vscode/ymz-vscode_0.0.1.vsix
```

需要 Node 18+。开发：打开 **`extensions/vscode` 文件夹**（不要从仓库根 F5），然后 F5。

## 发版如何挂上 VSIX

不写进 `.goreleaser.yaml` 的 `extra_files`（`goreleaser release --clean` 会清空 `dist/`）。

唯一发版脚本 [`scripts/publish-release.sh`](../../scripts/publish-release.sh) 在 Go 资产上传成功之后：

1. `scripts/package-vscode.sh vX.Y.Z` → `extensions/vscode/ymz-vscode_{version}.vsix`（把扩展 `package.json` 的 version 戳成 tag，不提交；`dist/` 可写时再拷一份）
2. `gh release upload` 挂到同一 Release（资产名仍是 `ymz-vscode_{version}.vsix`）

GitHub Actions [`.github/workflows/release.yml`](../../.github/workflows/release.yml) 同样在 goreleaser 之后打包装上。

本机无 Node：该步 **警告并跳过**，Go 二进制发版仍成功。不要另写 `scripts/release-*.sh`。

## 不做（本相）

Marketplace / Open VSX、内嵌 `ymzd`、Webview 聊天、Gateway HTTP、给 TUI 加 `--port`。
