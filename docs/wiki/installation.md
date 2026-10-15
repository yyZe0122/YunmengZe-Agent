# 安装与启动

从 [Releases](https://github.com/yyZe0122/YunmengZe-Agent/releases) 下载对应平台的包，核对同版本 `checksums.txt`，解压并把程序目录加入 `PATH`。入口只有 `ymz`（Windows 为 `ymz.exe`）。

| 平台 | 架构 | 包名（VERSION 不含 v） |
| --- | --- | --- |
| Linux / macOS | amd64、arm64 | `ymz_VERSION_linux_ARCH.tar.gz` / `ymz_VERSION_darwin_ARCH.tar.gz` |
| Windows | amd64、arm64 | `ymz_VERSION_windows_ARCH.zip` |

包内附带 README、许可证、配置示例、schema 和安装脚本。发布包应配合对应标签的文档使用；本 Wiki 跟随当前分支代码。[发布配置](../../.goreleaser.yaml)

## 从源码构建

需要 Go 1.26+，构建使用 `CGO_ENABLED=0`。在仓库根目录执行：

| 操作 | Linux / macOS（需要 make） | Windows PowerShell |
| --- | --- | --- |
| 构建 | `make build` | `./scripts/dev.ps1 -Action build` |
| 运行 | `./bin/ymz` | `./bin/ymz.exe` |
| 构建并安装 | `make install` | `./scripts/dev.ps1 -Action install` |
| 卸载程序 | `make uninstall` | `./scripts/dev.ps1 -Action uninstall` |

源码安装只提示 PATH。Unix 默认安装到 `~/.local/bin`，支持 `PREFIX` / `BINDIR`；Windows 支持 `-InstallDir` / `YMZ_INSTALL_DIR`，默认目录见下表。卸载保留配置与会话。

源码：[Makefile](../../Makefile)、[dev.ps1](../../scripts/dev.ps1)。

## 下载发布包的安装器

在仓库根目录运行 `sh packaging/scripts/install-user.sh` 或 `./packaging/scripts/install.ps1`。安装器下载并校验 SHA-256，创建缺失的配置和 env，保留已有文件。

| 设置 | Unix | Windows |
| --- | --- | --- |
| 安装目录 | `YMZ_INSTALL_DIR`；默认 `~/.local/bin` | `-InstallDir` / `YMZ_INSTALL_DIR`；默认 `%LOCALAPPDATA%\Programs\YunmengZe\bin`，无 LOCALAPPDATA 时为 `%USERPROFILE%\YunmengZe\bin` |
| 版本 | `YMZ_VERSION` | `-Version` / `YMZ_VERSION` |
| 下载仓库 | `YMZ_REPOSITORY` | `-Repository` / `YMZ_REPOSITORY` |
| 配置目录 | `YMZ_CONFIG_DIR` → `YMZ_HOME` → `~/.yunmengze` | `-ConfigDir` / `YMZ_CONFIG_DIR` → `YMZ_HOME` → `%USERPROFILE%\.yunmengze` |

版本默认 `latest`，无正式版时回退到发布列表。Unix 安装器更新 shell rc 的 PATH；Windows 更新用户 PATH，可用 `YMZ_NO_PATH_UPDATE=1` 禁用。修改后重新打开终端。

`YMZ_CONFIG_DIR` / `-ConfigDir` 只用于安装器；程序运行时只识别 `YMZ_HOME`，自定义配置目录时应同时设置它。

源码：[Unix 安装器](../../packaging/scripts/install-user.sh)、[Windows 安装器](../../packaging/scripts/install.ps1)、[Unix 卸载器](../../packaging/scripts/uninstall.sh)（只删除程序，支持 `YMZ_INSTALL_DIR`）。

## 首次启动

1. 在工作目录运行 `ymz`，程序会在 `$YMZ_HOME` 或 `~/.yunmengze` 创建缺失的 `agent.json`、`env`、`AGENTS.md`。
2. 默认模型为 `deepseek1/deepseek-chat`。在生成的 env 中填写 `DEEPSEEK1_API_KEY`，或按[配置说明](configuration.md)更换 provider。缺少密钥时首次启动会报错，模板已生成。
3. 执行 `ymz config validate` 检查配置，再运行 `ymz --mode plan`。脚本使用 `ymz run "任务"`；TUI 需要交互式终端。

MCP stdio 需要对应服务程序。源码：[CLI](../../cmd/ymz/main.go)、[配置初始化](../../internal/providerconfig/ensure.go)。
