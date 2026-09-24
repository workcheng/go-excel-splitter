# 版本升级与自动升级方案

## 1. GitHub 项目常见的版本升级方式

"版本升级"在 GitHub 项目里通常指三件事，要分开看：

### 1.1 项目自身发版（维护者侧）

| 方式 | 做法 | 适用场景 |
| --- | --- | --- |
| 手动打 tag | `git tag v1.2.0 && git push --tags` 触发 Release workflow | 小项目，**本仓库目前就是这种** |
| release-please | 按 Conventional Commits 自动维护一个 "Release PR"，合并后打 tag、写 CHANGELOG | 需要规范 changelog 的项目 |
| semantic-release | 每次合并到主分支后根据 commit 类型自动算版本号并发布 | 持续发布的库 |
| GoReleaser | 一个配置文件完成多平台构建、打包、checksums、签名、发布 Release/Homebrew/Scoop | Go 二进制项目的事实标准 |

版本号约定都是 SemVer：`MAJOR.MINOR.PATCH`，预发布用 `-beta.1` 之类的后缀。

### 1.2 依赖升级（仓库侧）

- **Dependabot**：`.github/dependabot.yml` 配置 `gomod`、`github-actions` 两个生态，定期提 PR。
- **Renovate**：功能更多（分组、自动合并、定时窗口）。
- 两者都要靠 CI 把关：依赖 PR 能自动合并的前提是测试可信。

### 1.3 用户端升级（使用者侧）

| 方式 | 说明 |
| --- | --- |
| 手动下载 | 用户去 Releases 页面下载，本项目目前只有这一种 |
| 包管理器 | Homebrew / Scoop / winget / apt，由包管理器负责升级 |
| 应用内自更新 | 程序检查 GitHub Releases，下载新版本并替换自身 —— **本方案重点** |

## 2. 本项目现状

- `release.yml`：推送 `v*` tag 时，在三个平台构建，上传 `excel-splitter-linux.tar.gz`、`excel-splitter-macos.tar.gz`、`excel-splitter-windows.zip`，用 `gh release create --generate-notes` 发布。
- 版本号分散在三处且互不关联：tag `v1.0.0`、`FyneApp.toml` 的 `Version = "1.0.0"`、`main.go` 里写死的 `"Excel闪电拆分工具 v1.0"`。**程序本身不知道自己是哪个版本**，这是做自动升级首先要解决的问题。
- Release 里没有 checksum 或签名文件，客户端无法校验下载内容。
- macOS 产物是裸二进制而不是 `.app`，也没有签名/公证。
- 仓库：`workcheng/go-excel-splitter`（公开），面向国内用户，GitHub 访问可能不稳定。

## 3. 自动升级方案

### 3.1 整体流程

```
启动 GUI ──(后台 goroutine，距上次检查 ≥24h)──► 请求 GitHub latest release
      │                                               │
      │                             新版本 > 当前版本且不在"跳过的版本"里？
      │                                               │ 是
      ▼                                               ▼
  正常使用  ◄──稍后 / 跳过此版本── 弹窗：显示版本号和更新说明
                                                      │ 立即更新
                                                      ▼
                             下载当前平台的包 → 校验 SHA256 → 解压
                                                      │
                             当前 exe 改名为 .old → 放入新 exe → 写 pending 标记
                                                      │
                             启动新进程 → 退出旧进程
                                                      │
                             新版本启动成功 → 清除标记、删除 .old
                             新版本启动失败 → 下次启动时回滚到 .old
```

### 3.2 触发条件

| 触发 | 行为 |
| --- | --- |
| GUI 启动 | 延迟约 5 秒后在后台检查，不阻塞界面；距上次检查不足 24 小时则跳过 |
| 菜单"检查更新" | 忽略 24 小时限制；没有新版本时也给出提示 |
| 命令行模式 | **不自动检查**（可能跑在脚本里）；提供 `excel-splitter --update` 手动更新 |
| 正在拆分 | 拆分任务进行中不弹窗、不替换，等任务结束再提示 |
| 开发构建 | `version == "dev"` 时不检查 |

### 3.3 版本检测

- **版本注入**：构建时通过 ldflags 写入，删掉代码里写死的版本号：

  ```go
  // main.go
  var version = "dev" // 构建时由 -ldflags "-X main.version=v1.2.0" 注入
  ```

  release.yml 两处 `go build` 加上 `-X main.version=${{ github.ref_name }}`，窗口标题和命令行输出都显示这个值。`FyneApp.toml` 的 Version 由发版流程同步修改（或者改为只在 `fyne package` 时用）。
- **获取最新版本**：`GET https://api.github.com/repos/workcheng/go-excel-splitter/releases/latest`
  - 这个接口天然会排除 draft 和 prerelease，正式通道直接用它；以后如果做 beta 通道，改用 `/releases` 列表自己筛选。
  - 未认证每个 IP 每小时限 60 次；每天检查一次完全够用。带上 `User-Agent`，收到 403/429 就静默跳过。
  - 超时 10 秒；失败完全静默，只写日志，不打扰用户。
- **比较**：用 `golang.org/x/mod/semver` 比较 `tag_name` 和 `version`，只有严格大于才提示。
- **资源匹配**：按 `runtime.GOOS` 找对应的资源名（`excel-splitter-windows.zip` 等），同时找 `checksums.txt`；缺少任意一个就当作没有可用更新。

### 3.4 升级步骤

1. **下载**：下载到系统临时目录，显示进度条，支持取消；限制最大体积（如 200MB），防止异常响应。
2. **校验**：下载 `checksums.txt`，核对压缩包的 SHA256，不一致就中止并删除临时文件。（进阶：用 minisign/cosign 给 `checksums.txt` 签名，公钥编进程序里，这样即使 Release 被篡改也能发现。）
3. **解压**：从 zip/tar.gz 中只取出预期文件名的那一个二进制，拒绝包含 `..` 的路径。
4. **替换**（兼容 Windows：运行中的 exe 不能被覆盖，但**可以改名**）：
   - `os.Executable()` 取当前路径，并解析符号链接；
   - 先检查所在目录是否可写，不可写（如装在 `Program Files`）就提示用户"请手动下载"并打开 Release 页面；
   - `excel-splitter.exe` → `excel-splitter.exe.old`，新文件移动到原路径（同一目录内 rename 是原子的），非 Windows 平台 `chmod 0755`；
   - 写入 `pending-update.json`：`{from, to, old_path, attempts: 0}`。
5. **重启**：用相同参数启动新进程，然后旧进程退出。

实现说明：最终没有使用 `go-selfupdate` 这类库，而是自己实现（`updater.go`）。因为启动健康检查和回滚本来就要自己写，自己实现只需额外依赖 `golang.org/x/mod/semver`。新可执行文件直接解压到程序同目录的 `.new`，保证替换是同一文件系统内的 rename。

### 3.5 回滚策略

分两层：

- **替换过程中失败**（移动新文件失败等）：立即把 `.old` 改回原名，提示用户更新失败，程序继续运行旧版本。
- **新版本启动失败**（启动健康检查）：
  - 程序启动时读取 `pending-update.json`，`attempts++` 后写回；
  - GUI 窗口正常显示并运行约 10 秒后，认为启动成功：删除标记和 `.old`；
  - 如果启动时发现 `attempts >= 2`（新版本已经连续两次没能到达"成功"点），就用 `.old` 替换回当前文件，把这个版本记入"跳过的版本"，重启进入旧版本，并提示"新版本启动失败，已回退"。
- **用户主动回退**：保留 Release 页面链接；不在程序内提供"降级"按钮，以免复杂化。

### 3.6 失败处理

| 失败点 | 处理 |
| --- | --- |
| 网络不通 / 超时 / 限流 | 静默跳过，24 小时后再试；手动检查时提示"无法连接更新服务器" |
| 下载中断 | 删除临时文件，提示可重试；不做断点续传 |
| 校验不通过 | 中止，删除文件，提示"下载文件损坏"；**绝不执行未经校验的文件** |
| 目录无写权限 | 改为打开 Release 页面，让用户手动下载 |
| 替换失败 | 按 3.5 立即回滚 |
| 杀毒软件拦截/删除 | 替换后检查新文件是否存在，不存在就回滚并提示 |
| 新版本崩溃 | 按 3.5 启动检查自动回滚 |

所有步骤写日志到用户配置目录（`os.UserConfigDir()/excel-splitter/update.log`），便于排查。

### 3.7 配置项

存放在 `os.UserConfigDir()/excel-splitter/update-state.json`（Windows 为 `%AppData%\excel-splitter\`），日志在同目录的 `update.log`。目前没有设置界面，需要时手动编辑该文件：

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `disable_auto_check` | `false` | 设为 `true` 关闭启动时的自动检查 |
| `last_check` | — | 上次检查时间 |
| `skipped_version` | — | 用户选择跳过的版本（自动回滚的版本也会写入这里） |
| `mirror` | 空 | 可选下载镜像前缀，改善国内访问（只用于下载，校验值始终以 GitHub 上的 checksums 为准） |
| `pending` / `rolled_back_from` | — | 内部状态：待确认的更新、上次回滚的版本 |

`beta` 通道暂未实现。

### 3.8 兼容性要点

- **Windows**：程序用 `-H=windowsgui` 构建，没有控制台，错误要用对话框展示；exe 被 SmartScreen 标记的问题需要代码签名证书才能根治；`.old` 文件在旧进程退出后才能删除，所以放到下次启动时清理。
- **macOS**：从网络下载的文件会带 `com.apple.quarantine` 属性，程序自己用 HTTP 下载的文件一般不会带，但未签名二进制仍可能被 Gatekeeper 拦截。如果以后改成分发 `.app`（`fyne package`），需要替换整个 `.app` 目录，而不是单个二进制，并需要签名+公证。**建议先确定 macOS 的分发形式再实现这一平台的自更新**，初期可以只提示并打开下载页。
- **Linux**：用户可能通过符号链接启动，或者装在 `/usr/local/bin` 这类无写权限的目录，这时退化为提示手动下载。
- **资源命名**：`excel-splitter-<os>.<ext>` 这个命名以后就是客户端的契约，改名会让旧版本找不到更新，要写进 release.yml 的注释。以后如果加 arm64 构建，要改成 `excel-splitter-<os>-<arch>`，同时保留旧名称至少一个版本。
- **架构**：当前三个平台都只构建 runner 的默认架构（macos-latest 是 arm64）。客户端必须校验 `runtime.GOARCH`，不匹配就不自动更新。
- **大版本**：如果新版本的 Release 说明里标了"需要手动升级"（如配置格式不兼容），就只提示不自动更新。可以约定 MAJOR 版本升级时只提示、不自动替换。

## 4. 落地步骤

| 步骤 | 改动 | 文件 |
| --- | --- | --- |
| 1. 版本可见 | 加 `var version = "dev"`，窗口标题和命令行输出使用它；release.yml 两处 go build 注入 `-X main.version=` | `main.go`、`release.yml` |
| 2. Release 附带校验文件 | publish job 在上传前执行 `sha256sum * > checksums.txt` | `release.yml` |
| 3. 检查更新 | 新文件 `updater.go`：查询 latest、比较 semver、匹配资源；GUI 增加"检查更新"按钮和启动时后台检查 | `updater.go`、`main.go` |
| 4. 下载与替换 | 接入 go-selfupdate（或自己实现），加进度对话框 | `updater.go` |
| 5. 回滚与健康检查 | `pending-update.json`、启动计数、成功后清理 | `updater.go` |
| 6. 依赖自动升级（可选） | 新增 `.github/dependabot.yml`：`gomod` 和 `github-actions` 每周检查 | `.github/dependabot.yml` |
| 7. 发版自动化（可选） | 引入 release-please 或 GoReleaser，统一版本号与 CHANGELOG | `.github/workflows/` |

**测试方法**：在 fork 仓库发一个 `v0.0.1` 和 `v0.0.2`，分别验证：正常升级、校验失败（手动改坏 checksums）、目录只读、断网、新版本启动即 `os.Exit(1)` 时的自动回滚。版本比较和资源匹配写成纯函数，做单元测试。

步骤 1、2 改动小、没有风险，建议先做；步骤 3 完成后用户就能收到更新提示，已经很有价值；步骤 4、5 再根据 macOS 分发形式决定范围。
