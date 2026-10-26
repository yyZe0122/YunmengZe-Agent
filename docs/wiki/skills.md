# 规则、技能与模板

源码：[规则](../../internal/providerconfig/agents.go)、[技能目录](../../internal/skillcatalog/catalog.go)、[模板](../../internal/providerconfig/chat.go)、[技能装配与消息展开](../../internal/agent/feature_skills.go)。

## 运行规则

每次运行开始依次读取 `<config>/AGENTS.md` 和 `<workspace>/.yunmengze/AGENTS.md`，每份读取最多 128 KiB、注入最多 8000 字符。只自动创建缺失的全局文件；仓库根目录 `AGENTS.md` 是贡献者指导，不在自动加载路径中。

非普通文件、直接符号链接、越界路径、读取或指令扫描失败的规则会被跳过。规则不改变 plan 的工具限制。

## 文件技能

用户技能在 `<config>/skills/<id>/SKILL.md`，项目技能在 `<workspace>/.yunmengze/skills/<id>/SKILL.md`；项目同 ID 优先。只发现一层目录，目录名就是调用 ID。最小示例：

```markdown
---
name: Review
description: Review the current change for correctness
---
Read the changed files and report concrete defects.
```

`name`、`description` 必填；元数据只支持单行 `key: value`，不是完整 YAML。发现阶段只读元数据，系统前缀只列出 ID 和描述；无效技能被跳过，并在运行 info 事件和 TUI `/skills` 中显示路径与原因。一次运行的工具、前缀及 slash 展开共用同一份发现结果。

发现阶段限制 frontmatter 总读取量为 1 MiB，技能 ID、名称和描述经过现有文本扫描。无效 UTF-8、隐藏控制字符或超出扫描预算的文本会被拒绝，可疑指令词只产生诊断。正文仍在明确加载时读取。

| 入口 | 用法 |
| --- | --- |
| `/skills` / `skills_list` | 查看技能目录 |
| `/<id> [参数]` | 把技能正文与参数展开为用户消息；CLI 可用 `ymz run "/review 当前修改"` |
| `skill_view` | `{"id":"review"}` 读取去掉 frontmatter 的正文；加 `"file_path":"references/checklist.md"` 读取目录内文件 |

关联文件必须是技能目录内的相对普通文件，不要求预先登记。拒绝越界、目标符号链接及 `SKILL.md`、`SKILL.md.draft`、`SKILL.md.bak.*`；技能目录和 SKILL.md 的直接符号链接也被跳过。正文与关联文件经过文本校验；可疑指令词只产生诊断，不因普通文本中出现 system 等词自动拒绝。模式限制和路径包含检查由代码独立执行。

技能编辑后，下次发现或读取即使用新内容。TUI 补全列表在初始化、加载会话和 `/new` 时重建，`/skills` 可重新查看目录。正文读取检查整个 SKILL.md 最多 1 MiB，关联文件也最多 1 MiB；传入文本扫描的正文或关联内容另受 200000 字符限制。`skill_view` 受工具输出上限限制，slash 展开不受该上限限制。

`chat.skills.enabled=false` 时不扫描技能目录，不注册技能工具，不提供技能补全或展开；手动输入 `/skills` 会提示禁用。运行规则和 chat.commands 模板仍可用。

## Slash 模板

在 `chat.commands` 中添加模板项，设置可选 `description` 和必填 `template`，例如：

```json
{"explain": {"description":"解释代码", "template":"解释以下代码：$ARGUMENTS"}}
```

名称只含字母、数字、`_`、`-`，不能与内置命令重名；模板非空且最多 8000 字符。描述和模板经过指令扫描。输入 `/explain internal/agent` 即生成消息，不执行脚本。

替换顺序：有 `$ARGUMENTS` 则替换它，否则替换 `$0`；都没有时隔一个空行追加参数。TUI 查找顺序为内置命令 → 配置模板 → 技能；模板先精确匹配再忽略大小写，技能 ID 精确匹配。
