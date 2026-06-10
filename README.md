# Excel 拆分工具

一个使用 Go、Fyne 和 excelize 开发的桌面工具，用于读取 Excel 文件，并按选中的表头列把数据拆分成多个 `.xlsx` 文件。

## 功能

- 支持选择或拖放 `.xlsx`、`.xls` 文件。
- 自动读取第一个工作表表头。
- 支持选择一个或多个分割列。
- 默认输出到源文件同级的 `文件名（拆分）` 文件夹。
- 按分组并行保存拆分后的 Excel 文件。

## 技术栈

- Go 1.25
- Fyne v2
- excelize v2

## 项目结构

```text
.
├── .github/workflows/     # GitHub CI 和 Release 工作流
├── app_icon.go            # 图标资源字节数据
├── FyneApp.toml           # Fyne 应用元信息
├── Icon.ico               # Windows 图标
├── Icon.png               # 应用图标
├── main.go                # GUI、Excel 读取和拆分逻辑
├── go.mod                 # Go 模块定义
├── go.sum                 # Go 依赖锁定
└── README.md              # 项目说明
```

## 本地开发

安装 Go 后，先下载依赖：

```bash
go mod download
```

运行测试：

```bash
go test ./...
```

启动应用：

```bash
go run .
```

构建当前平台版本：

```bash
go build -trimpath -ldflags="-s -w" -o excel-splitter .
```

Windows GUI 版本可使用：

```bash
go build -trimpath -ldflags="-s -w -H=windowsgui" -o excel-splitter.exe .
```

Linux 构建 Fyne 应用时需要系统 GUI 依赖，Ubuntu 可安装：

```bash
sudo apt-get update
sudo apt-get install -y gcc libgl1-mesa-dev xorg-dev
```

## 使用方式

1. 启动应用。
2. 点击“选择 Excel 文件”，或把 Excel 文件拖放到窗口中。
3. 选择需要作为拆分依据的列。
4. 选择输出文件夹，或使用默认输出文件夹。
5. 点击“开始拆分”。

## GitHub Actions

仓库包含两个工作流：

- `CI`：任意分支 push 或提交 Pull Request 时，在 Linux、macOS、Windows 上执行 `go test ./...`。
- `Release`：推送 `v*` 格式的 tag 时，在 Linux、macOS、Windows 上构建版本，并创建 GitHub Release。

发布新版本示例：

```bash
git tag v1.0.0
git push origin v1.0.0
```

Release 产物：

- `excel-splitter-linux.tar.gz`
- `excel-splitter-macos.tar.gz`
- `excel-splitter-windows.zip`

## 注意事项

- 当前拆分逻辑读取第一个工作表。
- 分割列为空时不会执行拆分。
- 输出文件名会自动替换 Windows 文件名中的非法字符。
- 大文件拆分耗时取决于数据量、磁盘速度和分组数量。

## 许可证

MIT License
