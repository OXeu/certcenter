# 提交规范

本仓库的提交信息遵循 [约定式提交 1.0.0](https://www.conventionalcommits.org/zh-hans/v1.0.0/)（Conventional Commits）。下文把规范原文与 FAQ 中与此仓库相关的部分整理为可执行的规则，并由"本仓库的约定"一节补充仓库自身的约束。二者冲突时，以本仓库的约定为准。

## 提交信息的结构

```
<类型>[可选 范围]: <描述>

[可选 正文]

[可选 脚注]
```

一个提交说明由三部分组成：实心的类型前缀行，其次是可选的正文，最后是可选的脚注。类型 `feat` 与 `fix` 在语义化版本（SemVer）中分别对应 `MINOR` 与 `PATCH`，带破坏性变更的提交对应 `MAJOR`。

## 规范条款

关键词"必须 / 禁止 / 应该 / 可以"按 RFC 2119 解释。

1. 每个提交都**必须**以类型字段开头：一个名词（如 `feat`、`fix`）加可选的范围、可选的 `!`、然后是一个英文冒号和一个空格。
2. 为应用或类库实现新功能时**必须**使用 `feat`；修复 bug 时**必须**使用 `fix`。
3. 范围**可以**跟在类型后面，**必须**是描述某部分代码的名词，用圆括号包围，例如 `fix(parser):`。
4. 描述**必须**紧跟在 `<类型>(范围)` 前缀的冒号和空格之后，是这次变更的简短总结，例如 `fix: array parsing issue when multiple spaces were contained in string`。
5. 简述之后**可以**写较长的正文，为变更提供额外上下文；正文**必须**与描述隔一个空行，段落之间**可以**再用空行分隔。
6. 正文结束的空行之后**可以**写一行或多行脚注。每个脚注**必须**由令牌（token）、后跟 `: ` 或 ` #` 作为分隔符、再跟令牌值组成（参考 git trailer 惯例）。
7. 脚注令牌**必须**用 `-` 连接（如 `Acked-by`），以便与多行正文区分；只有 `BREAKING CHANGE` 例外，它**可以**视为一个令牌。
8. 脚注值**可以**包含空格与换行；解析**必须**持续到下一个脚注令牌/分隔符出现为止。
9. 破坏性变更**必须**在提交信息中标记出来：在 `<类型>(范围)` 前缀中用 `!` 标记（此时描述**应该**说明破坏性变更，脚注中**可以**省略 `BREAKING CHANGE:`），或在脚注中写 `BREAKING CHANGE: <描述>`。含脚注时必须使用大写的 `BREAKING CHANGE`。`BREAKING-CHANGE` 是它的同义令牌。
10. `feat`、`fix` 之外**可以**使用其他类型，规范本身不做强制限制，它们在 SemVer 中没有隐式影响（除非带破坏性变更）。
11. 解析构成提交信息的各个单元时工具**必须**不区分大小写，只有 `BREAKING CHANGE` **必须**大写。
12. 类型的大小写**可以**任选，但**应该**保持一致。

### 推荐类型

除 `feat`、`fix` 外，采用 Community/Angular 约定推荐的类型，含义如下：

| 类型       | 用途                                                             |
| ---------- | ---------------------------------------------------------------- |
| `build`    | 修改项目的构建系统，如构建脚本、依赖库、外部接口、运行时版本升级 |
| `chore`    | 对非业务性代码的修改，如构建流程或工具配置                       |
| `ci`       | 修改持续集成流程的配置文件                                       |
| `docs`     | 修改文档，如 README、API 文档                                    |
| `style`    | 修改代码样式，如缩进、空格、空行，不改变逻辑                     |
| `refactor` | 重构，如修改代码结构、变量名、函数名但不修改功能逻辑             |
| `perf`     | 优化性能，如减少内存占用、提升执行速度                           |
| `test`     | 增删改测试用例                                                   |

### 还原提交（revert）

规范没有给还原提交下定义，把行为留给工具实现。推荐做法是使用 `revert` 类型，并在脚注中引用被还原的提交：

```
revert: let us never again speak of the noodle incident

Refs: 676104e, a215868
```

## 示例

包含正文与脚注、正文中说明破坏性变更的提交：

```
feat: allow provided config object to extend other configs

BREAKING CHANGE: `extends` key in config file is now used for extending other config files
```

用 `!` 标记破坏性变更：

```
feat!: send an email to the customer when a product is shipped

feat(api)!: send an email to the customer when a product is shipped
```

只有描述、没有正文的提交：

```
docs: correct spelling of CHANGELOG

feat(lang): add polish language
```

包含多行正文与多个脚注的提交：

```
fix: prevent racing of requests

Introduce a request id and a reference to latest request. Dismiss
incoming responses other than from latest request.

Remove timeouts which were used to mitigate the racing issue but are
obsolete now.

Reviewed-by: Z
Refs: #123
```

## 常见问题

- **提交符合多种类型时怎么办？** 回退并尽可能拆成多次提交。约定式提交的目的之一就是推动更有组织的提交和 PR。
- **写错了类型怎么办？** 在合并或发布之前，用 `git rebase -i` 修正历史；发布之后按所用工具和流程清理。若使用规范之外的类型，最坏结果只是被基于规范的工具忽略。
- **初始开发阶段如何提交？** 按"已经发布了产品"对待：总有人在使用你的软件，他们需要知道修复了什么、哪里不兼容。
- **这会阻碍快速迭代吗？** 它阻碍的是杂乱无章的快速前进，有助于跨项目、多贡献者长期快速演进。
- **所有贡献者都必须遵守吗？** 使用 squash 合并的工作流时，维护者可以在合并时整理提交信息；本仓库不在合并时改写作者提交，因此每次提交都按本规范书写。

## 本仓库的约定

以下是本仓库在规范之上增加的约束，与上文同样有约束力：

1. **提交信息使用英文。** 类型、范围、描述、正文、脚注一律英文，与代码和标识符保持一致；`docs/` 下的文档内容不受此限。
2. **类型小写，并限定在上述推荐类型内。** 需要新类型时，先修改本文件再使用。
3. **范围使用仓库内的目录或包名**，例如 `config`、`store`、`auth`、`oauth`、`server`、`mcp`、`i18n`、`apikey`、`slug`、`ui`、`sdk`、`tools`、`ci`；不确定时省略范围，不要使用宽泛或自造的范围名。
4. **描述不长于 72 个字符**，使用祈使语气（`add`、`fix`、`drop`），结尾不加句号。
5. **一个提交只做一件事。** 一次提交能对应多种类型时，按功能拆成多个提交，而不是勉强选定一个类型。
6. **不要用新的提交掩盖错误的提交信息。** 尚未合并的提交通过 `git rebase -i` 修正。

## 收到提交要求时的做法

用户要求提交（"commit"、"提交一下"、"提交"等）时，按以下流程执行，不询问先提交哪部分：

1. 先运行 `git status`，并审阅全部变更：已暂存、已修改、已删除和未跟踪的文件都要看。
2. 按**功能改动**把变更分组。每组是一次能独立描述的自洽改动，对应一个类型和一个描述。构建产物、锁文件、格式配置等改动归入引入或依赖它们的同一组。
3. 逐组执行 `git add <路径>` 并提交，提交信息符合本规范。禁止用 `git add -A` 或 `git commit -a` 把无关改动塞进同一个提交；同一个文件里的不同功能改动需要分开时，用 `git add -p`。
4. 不属于功能改动、也不应进入版本库的内容（本地环境文件、临时产物、数据库文件等）不提交；能被忽略规则排除的，补进 `.gitignore`，不要留在未跟踪状态。
5. 重复第 3、4 步，**直到 `git status` 输出 clean**（`git status --short` 为空）。被忽略的文件不算遗漏，但要确认它们确实应当被忽略。
6. **只要工作区里还有未提交的改动就不能停下**，不能只提交自己负责的那部分。无法归入任何功能改动的变更，要么作为 `chore:` 单独提交，要么向用户说明原因并等待指示，不得留在工作区。
7. 提交完成后报告：新增提交的列表（`git log --oneline`）以及 `git status --short` 的输出（必须为空）。
