# Excel转TXT工具

这是一个使用Go语言和Fyne框架开发的图形界面工具，用于将Excel文件（.xlsx或.xls）转换为TXT文件。

## 功能特性

- 📁 **文件选择**：支持通过拖放或按钮选择Excel文件
- 📊 **工作表选择**：可以选择要转换的特定工作表
- 🔄 **多种转换格式**：
  - 逐行转换：将Excel中每行数据转换为TXT中的一行
  - 逐列转换：将Excel中每列数据转换为TXT中的一行
  - 逐行逐列转换：将Excel中每个单元格数据单独一行
- 💾 **输出设置**：可自定义输出文件路径和名称
- 🎯 **实时预览**：显示当前选择的文件和转换状态

## 技术栈

- **Go语言**：主要开发语言
- **Fyne框架**：用于构建跨平台GUI界面
- **excelize**：用于读取Excel文件内容

## 安装方法

### 前提条件

- 安装Go 1.19或更高版本
- 确保系统已安装Git

### 克隆项目

```bash
git clone <repository-url>
cd excel-go
```

### 安装依赖

```bash
go mod tidy
```

### 构建应用

```bash
go build
# 命令行模式
go run main.go input.xlsx output_folder

# 编译GUI版本（Windows）
go build -ldflags="-s -w -H windowsgui" -o Excel拆分.exe main.go
# 编译命令行版本
go build -o excel-splitter main.go                                          
```

### 运行应用

```bash
./excel-go
```

## 使用说明

1. **选择Excel文件**：
   - 直接将Excel文件拖放到应用窗口
   - 或点击"选择文件"按钮浏览并选择文件

2. **选择工作表**：
   - 文件选择成功后，从下拉列表中选择要转换的工作表

3. **设置转换格式**：
   - 选择适合的转换格式（逐行、逐列或逐行逐列）

4. **选择输出路径**：
   - 点击"选择输出路径"按钮设置TXT文件的保存位置

5. **开始转换**：
   - 点击"转换"按钮开始转换过程
   - 转换完成后，会显示成功提示

## 项目结构

```
excel-go/
├── main.go           # 主程序代码
├── go.mod            # Go模块依赖
├── go.sum            # 依赖版本锁定
├── .gitignore        # Git忽略文件
└── README.md         # 项目说明文档
```

## 注意事项

1. 支持的Excel格式：.xlsx和.xls
2. 转换过程中请勿关闭应用窗口
3. 转换后的TXT文件编码为UTF-8
4. 大文件转换可能需要较长时间

## 常见问题

### 无法打开应用
- 确保已正确安装Go和所有依赖
- 检查系统是否支持Fyne框架（Windows、macOS、Linux均支持）

### 转换失败
- 检查Excel文件是否损坏
- 确保有足够的权限读写文件
- 尝试关闭其他可能正在使用该Excel文件的程序

## 许可证

[MIT License](LICENSE)

## 贡献

欢迎提交Issue和Pull Request！

## 联系方式

如有问题或建议，请通过以下方式联系：
- 项目地址：<repository-url>
- 开发者：[Your Name]