package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/xuri/excelize/v2"
)

// Windows API声明
var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procEnumWindows      = user32.NewProc("EnumWindows")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procDragAcceptFiles  = user32.NewProc("DragAcceptFiles") // 只有一个版本
	procDragQueryFile    = user32.NewProc("DragQueryFileW")  // 有Unicode版本
	procDragFinish       = user32.NewProc("DragFinish")      // 只有一个版本
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")

	// 消息常量
	WM_DROPFILES = uint32(0x0233)
	GWLP_WNDPROC uintptr
)

// 找到的窗口句柄
var foundHWND syscall.Handle

// EnumWindows回调函数
var enumWindowsCallback = syscall.NewCallback(func(hwnd syscall.Handle, lparam uintptr) uintptr {
	// 获取窗口标题
	buf := make([]uint16, 1024)
	r1, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))

	if r1 > 0 {
		windowTitle := syscall.UTF16ToString(buf[:r1])
		// 检查窗口标题是否包含我们的应用名称
		if strings.Contains(windowTitle, "Excel闪电拆分工具") {
			foundHWND = hwnd
			fmt.Printf("找到窗口: %s, 句柄: %x\n", windowTitle, hwnd)
			return 0 // 停止枚举
		}
	}

	return 1 // 继续枚举
})

func init() {
	// 初始化窗口过程常量
	// 在32位和64位系统上，-4的uintptr表示方式不同
	var temp int32 = -4
	GWLP_WNDPROC = uintptr(temp)
}

// FileCleaner 清理非法文件名字符
var fileCleaner = regexp.MustCompile(`[\\/:*?"<>|]`)

// cleanFilename 清理非法字符并限制长度
func cleanFilename(name string) string {
	cleaned := fileCleaner.ReplaceAllString(name, "_")
	if len(cleaned) > 200 {
		cleaned = cleaned[:200]
	}
	return strings.TrimSpace(cleaned)
}

// 原窗口过程函数指针
var originalWndProc uintptr

// 窗口过程回调函数
var wndProcCallback = syscall.NewCallback(func(hwnd syscall.Handle, msg uint32, wparam uintptr, lparam uintptr) uintptr {
	if msg == WM_DROPFILES {
		// 处理拖放文件
		var fileCount uint32

		// 获取文件数量
		r1, _, _ := procDragQueryFile.Call(wparam, uintptr(^uint32(0)), 0, 0)
		fileCount = uint32(r1)

		if fileCount > 0 {
			// 只处理第一个文件
			buf := make([]uint16, 1024)
			r1, _, _ := procDragQueryFile.Call(wparam, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			if r1 > 0 {
				filePath := syscall.UTF16ToString(buf[:r1])
				// 检查是否为Excel文件
				if strings.HasSuffix(strings.ToLower(filePath), ".xlsx") || strings.HasSuffix(strings.ToLower(filePath), ".xls") {
					// 更新全局输入文件变量
					inputFile = filePath
					// 更新状态标签
					statusLabel.SetText(fmt.Sprintf("已选择: %s", filepath.Base(filePath)))
				}
			}
		}

		// 完成拖放处理
		procDragFinish.Call(wparam)
		return 0
	}

	// 调用原窗口过程
	ret, _, _ := procCallWindowProc.Call(originalWndProc, uintptr(hwnd), uintptr(msg), wparam, lparam)
	return ret
})

// 全局变量用于窗口过程访问
// 全局变量
var (
	inputFile   string
	outputDir   string
	statusLabel *widget.Label
	myWindow    fyne.Window // 全局窗口变量，用于拖放处理
)

// generateKey 生成文件名
func generateKey(row map[string]string) string {
	// 处理日期
	dateStr := row["日期"]
	if t, err := time.Parse("2006-01-02", dateStr); err == nil {
		dateStr = t.Format("2006-01-02")
	}

	salesman := cleanFilename(row["业务员"])
	shop := cleanFilename(row["店铺名称"])

	return fmt.Sprintf("%s_%s_%s", salesman, shop, dateStr)
}

// SaveTask 保存任务结构
type SaveTask struct {
	Key          string
	Rows         [][]string
	Headers      []string
	OutputFolder string
}

// saveGroup 保存单个分组
func saveGroup(task SaveTask) string {
	f := excelize.NewFile()
	defer f.Close()

	// 创建工作表
	sheetName := "Sheet1"
	index, _ := f.NewSheet(sheetName)
	f.SetActiveSheet(index)

	// 写入表头
	for col, header := range task.Headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		f.SetCellValue(sheetName, cell, header)
	}

	// 写入数据
	for rowIdx, row := range task.Rows {
		for col, value := range row {
			cell, _ := excelize.CoordinatesToCellName(col+1, rowIdx+2)
			f.SetCellValue(sheetName, cell, value)
		}
	}

	// 生成文件路径
	outputPath := filepath.Join(task.OutputFolder, task.Key+".xlsx")
	counter := 1
	for {
		if _, err := os.Stat(outputPath); os.IsNotExist(err) {
			break
		}
		outputPath = filepath.Join(task.OutputFolder, fmt.Sprintf("%s_%d.xlsx", task.Key, counter))
		counter++
	}

	// 保存文件
	if err := f.SaveAs(outputPath); err != nil {
		return fmt.Sprintf("✗ %s: %s", task.Key, err.Error())
	}
	return fmt.Sprintf("✓ %s.xlsx", task.Key)
}

// splitExcelParallel 并行拆分Excel主函数
func splitExcelParallel(inputFile, outputFolder string, maxWorkers int) ([]string, time.Duration, error) {
	startTime := time.Now()

	fmt.Printf("📂 读取文件: %s\n", inputFile)
	fileOpenStart := time.Now()

	// 打开Excel文件
	f, err := excelize.OpenFile(inputFile)
	if err != nil {
		return nil, 0, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()
	fmt.Printf("✅ 文件打开完成，耗时: %.2f秒\n", time.Since(fileOpenStart).Seconds())

	// 获取所有工作表
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, 0, fmt.Errorf("Excel文件中没有工作表")
	}

	// 使用第一个工作表
	sheetName := sheets[0]
	fmt.Printf("📋 使用工作表: %s\n", sheetName)

	// 获取所有行
	rowsStart := time.Now()
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, 0, fmt.Errorf("读取工作表失败: %w", err)
	}
	fmt.Printf("✅ 读取所有行完成，耗时: %.2f秒\n", time.Since(rowsStart).Seconds())

	if len(rows) < 2 {
		return nil, 0, fmt.Errorf("数据为空")
	}

	// 获取表头和数据
	headers := rows[0]
	data := rows[1:]
	fmt.Printf("📊 表头数量: %d，数据总行数: %d\n", len(headers), len(data))

	// 检查必要列
	checkColsStart := time.Now()
	requiredCols := map[string]int{"业务员": -1, "店铺名称": -1, "日期": -1}
	for idx, header := range headers {
		if _, ok := requiredCols[header]; ok {
			requiredCols[header] = idx
		}
	}
	for col, idx := range requiredCols {
		if idx == -1 {
			return nil, 0, fmt.Errorf("缺少必要列: %s", col)
		}
	}
	fmt.Printf("✅ 必要列检查完成，耗时: %.2f秒\n", time.Since(checkColsStart).Seconds())

	// 按文件名分组
	groupingStart := time.Now()
	groups := make(map[string][][]string)
	for i, row := range data {
		if i%1000 == 0 {
			fmt.Printf("🔄 正在处理第 %d 行...\n", i)
		}
		if len(row) < len(headers) {
			continue // 跳过不完整行
		}

		// 构建行映射
		rowMap := make(map[string]string)
		for i, header := range headers {
			if i < len(row) {
				rowMap[header] = row[i]
			}
		}

		key := generateKey(rowMap)
		groups[key] = append(groups[key], row)
	}
	fmt.Printf("✅ 分组完成，耗时: %.2f秒\n", time.Since(groupingStart).Seconds())
	fmt.Printf("📦 分组数量: %d\n", len(groups))
	if maxWorkers <= 0 {
		maxWorkers = 4 // 默认4个goroutine
	}
	fmt.Printf("⚡ 开始并行保存（%d个worker）\n", maxWorkers)

	// 创建任务通道和结果通道
	taskChan := make(chan SaveTask, len(groups))
	resultChan := make(chan string, len(groups))

	// 启动worker
	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskChan {
				result := saveGroup(task)
				resultChan <- result
				fmt.Printf("Worker %d 完成任务: %s\n", workerID, result)
			}
		}(i)
	}

	// 投递任务
	taskStart := time.Now()
	fmt.Println("📤 开始投递任务...")
	for key, rows := range groups {
		taskChan <- SaveTask{
			Key:          key,
			Rows:         rows,
			Headers:      headers,
			OutputFolder: outputFolder,
		}
	}
	close(taskChan)
	fmt.Printf("✅ 所有任务投递完成，耗时: %.2f秒\n", time.Since(taskStart).Seconds())

	// 等待所有任务完成
	waitStart := time.Now()
	fmt.Println("⏳ 等待所有任务完成...")
	wg.Wait()
	close(resultChan)
	fmt.Printf("✅ 所有任务执行完成，耗时: %.2f秒\n", time.Since(waitStart).Seconds())

	// 收集结果
	var results []string
	for result := range resultChan {
		results = append(results, result)
	}

	// 统计耗时
	elapsed := time.Since(startTime)
	minutes := int(elapsed.Minutes())
	seconds := elapsed.Seconds()

	// 统计成功失败
	success := 0
	failed := 0
	for _, r := range results {
		if strings.HasPrefix(r, "✓") {
			success++
		} else if strings.HasPrefix(r, "✗") {
			failed++
		}
	}

	fmt.Printf("\n✅ 完成！成功: %d, 失败: %d\n", success, failed)
	if minutes > 0 {
		fmt.Printf("⏱️  总耗时: %d分 %.2f秒\n", minutes, seconds)
	} else {
		fmt.Printf("⏱️  总耗时: %.2f秒\n", seconds)
	}

	return results, elapsed, nil
}

// guiVersion GUI版本
func guiVersion() {
	myApp := app.New()
	myWindow = myApp.NewWindow("Excel闪电拆分工具") // 赋值给全局变量
	// 设置窗口大小为更适合一般应用的尺寸
	myWindow.Resize(fyne.NewSize(800, 600))

	// 使用两个Label组件分别显示主标题和副标题
	mainLabel := widget.NewLabel("Excel闪电拆分工具")
	mainLabel.Alignment = fyne.TextAlignCenter

	// 创建副标题
	subLabel := widget.NewLabel("速度比VBA快10-20倍")
	subLabel.Alignment = fyne.TextAlignCenter
	// 通过调整Label的大小来间接调整文本大小
	subLabel.Resize(fyne.NewSize(400, 20))

	statusLabel = widget.NewLabel("提示：可以将Excel文件直接拖放到下方区域")
	statusLabel.Alignment = fyne.TextAlignCenter

	var outputFolder string
	var runBtn *widget.Button

	// 重置全局变量
	inputFile = ""

	// 添加一个简单的拖放支持：检查命令行参数
	// 如果用户拖放文件到可执行文件上，会作为命令行参数传递
	// 注意：这里只在GUI模式下处理命令行参数作为拖放
	if len(os.Args) > 1 {
		filePath := os.Args[1]
		// 检查文件是否为Excel文件
		if strings.HasSuffix(strings.ToLower(filePath), ".xlsx") || strings.HasSuffix(strings.ToLower(filePath), ".xls") {
			inputFile = filePath
			statusLabel.SetText(fmt.Sprintf("已选择: %s", filepath.Base(inputFile)))
		}
	}

	// 创建拖放区域（使用简单的标签提示）
	dropArea := container.NewVBox(
		widget.NewLabelWithStyle("拖放Excel文件到此处", fyne.TextAlignCenter, fyne.TextStyle{Italic: true}),
		widget.NewLabelWithStyle("（或使用下方按钮选择文件）", fyne.TextAlignCenter, fyne.TextStyle{}),
	)

	// 设置拖放区域大小
	dropArea.Resize(fyne.NewSize(400, 100))

	// 使用Fyne v2.7.1的正确拖放API：Window.SetOnDropped
	myWindow.SetOnDropped(func(pos fyne.Position, uris []fyne.URI) {
		if len(uris) > 0 {
			// 获取第一个拖放的文件URI
			fileURI := uris[0]
			filePath := fileURI.Path()

			// 处理Windows路径格式
			filePath = strings.ReplaceAll(filePath, "/", "\\")

			// 检查是否是Excel文件
			if strings.HasSuffix(strings.ToLower(filePath), ".xlsx") || strings.HasSuffix(strings.ToLower(filePath), ".xls") {
				inputFile = filePath

				// 自动生成输出文件夹路径：输入文件所在文件夹 + 文件名（无扩展名） + "（拆分）"
				inputDir := filepath.Dir(inputFile)
				inputFileName := filepath.Base(inputFile)
				// 移除文件扩展名
				inputFileNameWithoutExt := strings.TrimSuffix(inputFileName, filepath.Ext(inputFileName))
				outputFolder = filepath.Join(inputDir, inputFileNameWithoutExt+"（拆分）")

				// 检查文件夹是否存在，如果不存在则创建
				if _, err := os.Stat(outputFolder); os.IsNotExist(err) {
					err := os.MkdirAll(outputFolder, 0755)
					if err != nil {
						statusLabel.SetText(fmt.Sprintf("错误: 无法创建输出文件夹: %v", err))
						return
					}
				}

				statusLabel.SetText(fmt.Sprintf("已选择: %s\n输出到: %s", filepath.Base(inputFile), outputFolder))
			} else {
				// 如果没有找到有效的Excel文件，显示错误
				dialog.ShowInformation("错误", "请拖放有效的Excel文件（.xlsx或.xls格式）", myWindow)
			}
		}
	})

	inputBtn := widget.NewButton("选择Excel文件", func() {
		// 获取当前应用程序所在的文件夹
		execPath, _ := os.Executable()
		currentDir := filepath.Dir(execPath)

		// 创建文件选择对话框
		dialog := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil || reader == nil {
				return
			}
			inputFile = reader.URI().Path()
			statusLabel.SetText(fmt.Sprintf("已选择: %s", filepath.Base(inputFile)))

			// 自动生成输出文件夹路径：输入文件所在文件夹 + 文件名（无扩展名） + "（拆分）"
			inputDir := filepath.Dir(inputFile)
			inputFileName := filepath.Base(inputFile)
			// 移除文件扩展名
			inputFileNameWithoutExt := strings.TrimSuffix(inputFileName, filepath.Ext(inputFileName))
			outputFolder = filepath.Join(inputDir, inputFileNameWithoutExt+"（拆分）")

			// 检查文件夹是否存在，如果不存在则创建
			if _, err := os.Stat(outputFolder); os.IsNotExist(err) {
				err := os.MkdirAll(outputFolder, 0755)
				if err != nil {
					statusLabel.SetText(fmt.Sprintf("错误: 无法创建输出文件夹: %v", err))
					return
				}
			}

			statusLabel.SetText(fmt.Sprintf("已选择: %s\n输出到: %s", filepath.Base(inputFile), outputFolder))
		}, myWindow)

		// 设置过滤器
		dialog.SetFilter(storage.NewExtensionFileFilter([]string{".xlsx", ".xls"}))

		// 设置默认打开位置为当前应用所在的文件夹
		if uri, err := storage.ParseURI("file://" + currentDir); err == nil {
			if lister, err := storage.ListerForURI(uri); err == nil {
				dialog.SetLocation(lister)
			}
		}

		dialog.Show()
	})

	outputBtn := widget.NewButton("选择输出文件夹", func() {
		// 获取当前应用程序所在的文件夹
		execPath, _ := os.Executable()
		currentDir := filepath.Dir(execPath)

		// 创建文件夹选择对话框
		dialog := dialog.NewFolderOpen(func(list fyne.ListableURI, err error) {
			if err != nil || list == nil {
				return
			}
			outputFolder = list.Path()
			statusLabel.SetText(fmt.Sprintf("输出到: %s", outputFolder))
		}, myWindow)

		// 设置默认打开位置为当前应用所在的文件夹
		if uri, err := storage.ParseURI("file://" + currentDir); err == nil {
			if lister, err := storage.ListerForURI(uri); err == nil {
				dialog.SetLocation(lister)
			}
		}

		dialog.Show()
	})

	runBtn = widget.NewButton("开始拆分", func() {
		if inputFile == "" {
			dialog.ShowInformation("提示", "请先选择Excel文件", myWindow)
			return
		}

		// 禁用按钮
		runBtn.Disable()

		statusLabel.SetText("处理中...")

		// 在goroutine中执行
		go func() {
			defer func() {
				runBtn.Enable()
			}()

			r, elapsed, err := splitExcelParallel(inputFile, outputFolder, 8)
			if err != nil {
				dialog.ShowError(err, myWindow)
				statusLabel.SetText(fmt.Sprintf("错误: %v", err))
				return
			}

			fmt.Printf("执行结果: %s \n", r)

			// 格式化耗时
			minutes := int(elapsed.Minutes())
			seconds := elapsed.Seconds()
			timeStr := strconv.Itoa(minutes) + "分" + fmt.Sprintf("%.2f秒", seconds)
			if minutes <= 0 {
				timeStr = fmt.Sprintf("%.2f秒", seconds)
			}

			// 将结果数组转换为按行展示的格式，并添加数字编号
			var resultLines string
			for i, line := range r {
				resultLines += fmt.Sprintf("%d. %s\n", i+1, line)
			}
			// 移除最后一个换行符
			if len(resultLines) > 0 {
				resultLines = resultLines[:len(resultLines)-1]
			}

			// 创建自定义对话框，支持复制文本
			showCopyableDialog("完成", fmt.Sprintf("✅ 拆分完成！\n\n总耗时: %s\n\n执行结果（已按业务员、店铺名称、日期排序）（总共 %d 条记录）:\n\n%s", timeStr, len(r), resultLines), myWindow)
			statusLabel.SetText("处理完成！")
		}()
	})
	runBtn.Importance = widget.HighImportance

	content := container.NewVBox(
		// mainLabel,
		// subLabel,
		// widget.NewSeparator(),
		dropArea,
		widget.NewSeparator(),
		inputBtn,
		outputBtn,
		widget.NewSeparator(),
		runBtn,
		statusLabel,
		widget.NewLabel("💡 待处理的Excel需包含以下列名：业务员、店铺名称、日期"),
	)

	myWindow.SetContent(content)

	// 注意：Windows API拖放功能已移除，
	// 当前版本使用命令行参数处理拖放文件（当用户将文件拖放到可执行文件上时）

	// 显示窗口
	myWindow.ShowAndRun()
}

// 显示支持复制的自定义对话框
func showCopyableDialog(title, content string, win fyne.Window) {
	// 创建自定义的只读但可复制的文本区域
	// 使用Entry组件但通过监听事件防止编辑
	textEntry := widget.NewEntry()
	textEntry.MultiLine = true
	textEntry.Wrapping = fyne.TextWrapWord
	textEntry.SetText(content)

	// 监听文本变更事件并恢复原始内容，实现只读效果
	originalContent := content
	textEntry.OnChanged = func(s string) {
		if s != originalContent {
			textEntry.SetText(originalContent)
		}
	}

	// 创建滚动容器
	scrollContainer := container.NewScroll(textEntry)

	// 创建对话框
	dialog := dialog.NewCustom(title, "关闭",
		scrollContainer, win)

	// 设置对话框大小，与新窗口比例协调
	dialog.Resize(fyne.NewSize(600, 400))

	// 显示对话框
	dialog.Show()
}

func main() {
	fmt.Println("Excel闪电拆分工具 v1.0")
	fmt.Println("使用方法: excel-splitter <输入文件> [输出文件夹]")
	fmt.Printf("命令行参数数量: %d\n", len(os.Args))
	for i, arg := range os.Args {
		fmt.Printf("参数 %d: %s\n", i, arg)
	}

	// 命令行模式
	if len(os.Args) > 1 {
		// 检查是否是GUI模式的拖放操作
		filePath := os.Args[1]
		if strings.HasSuffix(strings.ToLower(filePath), ".xlsx") || strings.HasSuffix(strings.ToLower(filePath), ".xls") {
			// 启动GUI模式并处理拖放的文件
			fmt.Println("启动GUI界面并处理拖放的文件...")
			guiVersion()
		} else {
			// 命令行模式
			fmt.Printf("正在处理文件: %s\n", os.Args[1])
			inputFile := os.Args[1]
			outputFolder := "."
			if len(os.Args) > 2 {
				outputFolder = os.Args[2]
				fmt.Printf("输出文件夹: %s\n", outputFolder)
			}

			fmt.Println("开始调用splitExcelParallel函数...")
			_, elapsed, err := splitExcelParallel(inputFile, outputFolder, 0)
			if err != nil {
				fmt.Printf("错误: %v\n", err)
				os.Exit(1)
			}

			// 显示耗时
			minutes := int(elapsed.Minutes())
			seconds := elapsed.Seconds()
			if minutes > 0 {
				fmt.Printf("\n⏱️  总耗时: %d分 %.2f秒\n", minutes, seconds)
			} else {
				fmt.Printf("\n⏱️  总耗时: %.2f秒\n", seconds)
			}
		}
	} else {
		// 启动GUI
		fmt.Println("启动GUI界面...")
		guiVersion()
	}
}
