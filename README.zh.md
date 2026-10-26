# 云梦泽 Agent

用 Go 编写的终端编程 Agent。通过交互式界面或命令行，理解项目、编辑文件并执行命令。

[English](README.md) · [使用文档](docs/wiki/README.md) · [下载发布包](https://github.com/yyZe0122/YunmengZe-Agent/releases) · [参与贡献](CONTRIBUTING.md)

## 功能

- **终端交互**：切换模型、恢复会话、运行中补充要求，并直接回答 Agent 的澄清问题。
- **两种执行模式**：使用 `plan` 阅读和规划，使用 `agent` 修改文件、运行命令。
- **自由选择模型**：支持 OpenAI 兼容 Chat Completions、Responses、Anthropic Messages 和 Gemini，也可连接兼容协议的本地服务。
- **本地保存会话**：会话、上下文摘要和记忆存入同一个 SQLite 数据库，支持长工具任务的上下文压缩，并低频整理有证据的用户偏好。
- **可选功能**：加载文件技能和 MCP 服务、最多并行执行两个独立子任务、搜索网页，并使用当前模型或单独配置的图像模型识图；每项能力均可通过配置关闭。
- **单个可执行文件**：`ymz` 支持 Windows、Linux 和 macOS，无需单独启动后台服务。

会话数据保存在本地；模型调用以及启用的网页、图像和 MCP 工具会访问所配置的服务。

## 安装

发布包支持 Windows、Linux、macOS 的 amd64 和 arm64 架构；仅从源码构建时需要安装 Go。

**Windows — [Scoop](https://scoop.sh)：**

```powershell
scoop bucket add ymz https://github.com/yyZe0122/scoop-bucket
scoop install ymz/ymz
```

**macOS — [Homebrew](https://brew.sh)：**

```sh
brew install --cask yyZe0122/tap/ymz
```

**Linux / macOS — 用户安装脚本：**

```sh
curl -fsSL https://raw.githubusercontent.com/yyZe0122/YunmengZe-Agent/main/packaging/scripts/install-user.sh | sh
```

**Windows PowerShell — 不使用 Scoop 的安装方式：**

```powershell
irm https://raw.githubusercontent.com/yyZe0122/YunmengZe-Agent/main/packaging/scripts/install.ps1 | iex
```

用户安装脚本下载最新正式版、校验 SHA-256，并保留已有配置。安装后重新打开终端，运行 `ymz version` 确认版本。

也可以在 [Releases](https://github.com/yyZe0122/YunmengZe-Agent/releases) 下载压缩包，使用 `checksums.txt` 校验后，将 `ymz`（Windows 为 `ymz.exe`）放入 `PATH`。

<details>
<summary>从源码构建</summary>

从当前源码构建需要 **Go 1.26+**。先克隆仓库：

```sh
git clone https://github.com/yyZe0122/YunmengZe-Agent.git
cd YunmengZe-Agent
```

Linux / macOS：

```sh
make build
./bin/ymz
```

Windows PowerShell：

```powershell
./scripts/dev.ps1 -Action build
./bin/ymz.exe
```

</details>

更新命令、安装目录和版本选择见[安装说明](docs/wiki/installation.md)。升级已有安装前，请先阅读对应版本的发布说明。

## 快速开始

1. 运行一次 `ymz`，在 `~/.yunmengze` 创建配置文件；Windows 对应 `$env:USERPROFILE\.yunmengze`。
2. 编辑 `agent.json`，选择 provider 和模型，并在 `env` 文件中填写密钥。默认示例使用 `deepseek1/deepseek-chat` 和 `DEEPSEEK1_API_KEY`；密钥未填写时，首次启动会报缺少环境变量。
3. 执行 `ymz config validate` 检查本地配置，再在项目目录启动 `ymz`，输入需要完成的任务。该检查不会请求模型服务。

```sh
ymz
ymz --mode plan
ymz --continue
ymz run --mode plan "解释这个项目的结构"
ymz run --json "总结当前工作区的修改"
```

使用 `ymz models` 列出配置的主模型、角色模型和 provider 目录模型，不要求密钥齐全。

## 配置

通过 `YMZ_HOME` 可以更换配置目录，`core.db` 也保存在该目录。`agent.local.json` 优先于 `agent.json`，两者不合并。其他 provider 和模型可参照[配置示例](configs/agent.json.example)填写。

以下开关默认均为 `true`，省略时也保持启用。设为 `false` 可关闭对应能力，已有会话和记忆数据继续保留。

| 能力 | 配置项 |
| --- | --- |
| 文件技能 | `chat.skills.enabled` |
| 用户偏好记忆 | `chat.memory.enabled` |
| 子任务 | `chat.task.enabled` |
| 网页搜索、提取及 web 子任务 | `chat.web.enabled` |
| 图像分析 | `chat.vision.enabled` |
| MCP 集成 | `mcp.enabled` |
| 自动上下文压缩 | `chat.compaction.enabled` |

关闭自动压缩后仍可使用 `/compact` 手动压缩。关闭网页工具不会限制 shell、MCP 服务或图片 URL 联网。

只有主模型或已启用功能的凭证引用缺失才阻止启动；其他受影响模型标为不可用，选择时显示原因。配置、已解析凭证和解析错误均在启动时固定，修改后需重启。模型角色、默认值和启用依赖见[配置文档](docs/wiki/configuration.md)。

## 使用方式

| 模式 | 行为 |
| --- | --- |
| `agent`，默认 | 使用当前操作系统账户的权限执行工具，包括修改文件和运行 shell，不弹出审批请求。 |
| `plan` | 允许阅读、搜索与讨论；工具注册表阻止文件修改、shell、记忆修改和 MCP。 |

TUI 空闲时按 **Tab** 切换模式，**Ctrl+L** 选择模型，**Ctrl+S** 恢复会话。**Enter** 发送消息，运行中则排队补充要求；**Esc** 取消本轮。输入 `/help` 查看命令。

脚本调用使用 `ymz run`，参数放在任务文本之前；`--json` 逐行输出 JSON 事件。完整命令和事件见 [CLI 与 TUI 使用说明](docs/wiki/usage.md)，模式的具体限制见[执行边界](SECURITY.md)。

## 文档

- [安装与启动](docs/wiki/installation.md)、[配置](docs/wiki/configuration.md)
- [工具](docs/wiki/tools.md)、[技能](docs/wiki/skills.md)、[记忆](docs/wiki/memory.md)
- [架构](docs/wiki/architecture.md)、[Provider 适配](docs/wiki/provider-protocols.md)、[数据库](docs/wiki/database.md)
- [贡献指南](CONTRIBUTING.md)、[发布流程](docs/release.md)

CLI 和 TUI 共用进程内的 Engine。可选能力通过显式装配和启动开关组合；从源码移除能力还需调整对应配置、提示和界面入口。具体见[模块边界](docs/wiki/architecture.md#模块边界与可选能力)与[当前审核及优化方案](docs/backlog/current.md)。

Wiki 对应当前检出的代码；使用发布包时，应查看对应 Git 标签下的文档。

## 许可证

[Apache License 2.0](LICENSE)。
