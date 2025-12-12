package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/xuri/excelize/v2"
)

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
	myWindow := myApp.NewWindow("Excel闪电拆分工具")
	myWindow.Resize(fyne.NewSize(450, 250))

	label := widget.NewLabel("Excel闪电拆分工具\n速度比VBA快10-20倍")
	label.Alignment = fyne.TextAlignCenter

	statusLabel := widget.NewLabel("提示：可以将Excel文件直接拖放到窗口中")
	statusLabel.Alignment = fyne.TextAlignCenter

	var inputFile string
	var outputFolder string
	var runBtn *widget.Button

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

	inputBtn := widget.NewButton("选择Excel文件", func() {
		dialog := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil || reader == nil {
				return
			}
			inputFile = reader.URI().Path()
			statusLabel.SetText(fmt.Sprintf("已选择: %s", filepath.Base(inputFile)))
		}, myWindow)
		dialog.SetFilter(storage.NewExtensionFileFilter([]string{".xlsx", ".xls"}))
		dialog.Show()
	})

	outputBtn := widget.NewButton("选择输出文件夹", func() {
		dialog := dialog.NewFolderOpen(func(list fyne.ListableURI, err error) {
			if err != nil || list == nil {
				return
			}
			outputFolder = list.Path()
			statusLabel.SetText(fmt.Sprintf("输出到: %s", outputFolder))
		}, myWindow)
		dialog.Show()
	})

	runBtn = widget.NewButton("开始拆分", func() {
		if inputFile == "" || outputFolder == "" {
			dialog.ShowInformation("提示", "请先选择Excel文件和输出文件夹", myWindow)
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

			_, elapsed, err := splitExcelParallel(inputFile, outputFolder, 8)
			if err != nil {
				dialog.ShowError(err, myWindow)
				statusLabel.SetText(fmt.Sprintf("错误: %v", err))
				return
			}

			// 格式化耗时
			minutes := int(elapsed.Minutes())
			seconds := elapsed.Seconds()
			timeStr := strconv.Itoa(minutes) + "分" + fmt.Sprintf("%.2f秒", seconds)
			if minutes <= 0 {
				timeStr = fmt.Sprintf("%.2f秒", seconds)
			}

			dialog.ShowInformation("完成", fmt.Sprintf("✅ 拆分完成！\n\n总耗时: %s", timeStr), myWindow)
			statusLabel.SetText("处理完成！")
		}()
	})
	runBtn.Importance = widget.HighImportance

	content := container.NewVBox(
		label,
		widget.NewSeparator(),
		inputBtn,
		outputBtn,
		widget.NewSeparator(),
		runBtn,
		statusLabel,
		widget.NewLabel("需要处理的excel,需要包含列名：业务员、店铺名称、日期"),
	)

	myWindow.SetContent(content)
	myWindow.ShowAndRun()
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
