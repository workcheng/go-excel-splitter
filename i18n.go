package main

import (
	"errors"
	"fmt"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

const (
	languageChinese = "zh-CN"
	languageEnglish = "en"
)

var (
	languageMu         sync.RWMutex
	currentLanguage    = languageChinese
	refreshLocalizedUI func()
)

var englishText = map[string]string{
	"Excel闪电拆分工具":            "Excel Splitter",
	"速度比VBA快10-20倍":          "10-20x faster than VBA",
	"提示：可以将Excel文件直接拖放到下方区域": "Tip: drag and drop an Excel file below",
	"请选择Excel文件后选择分割列（可多选）":  "Choose an Excel file, then select split columns",
	"选择分割列（可多选，共 %d 列）":      "Select split columns (%d available)",
	"拖放Excel文件到此处":           "Drop an Excel file here",
	"（或使用下方按钮选择文件）":          "(or use the button below to choose a file)",
	"选择Excel文件":              "Choose Excel file",
	"选择输出文件夹":                "Choose output folder",
	"开始拆分":                   "Split",
	"处理中...":                 "Processing...",
	"处理完成！":                  "Completed!",
	"检查更新":                   "Check for updates",
	"打赏作者":                   "Support the author",
	"设置":                     "Settings",
	"语言":                     "Language",
	"关闭":                     "Close",
	"简体中文":                   "简体中文",
	"应用语言":                   "Application language",
	"版本 %s":                  "Version %s",
	"已选择: %s\n输出到: %s":       "Selected: %s\nOutput: %s",
	"输出到: %s":                "Output: %s",
	"错误":                     "Error",
	"提示":                     "Notice",
	"请先选择Excel文件":            "Choose an Excel file first",
	"请至少选择一个分割列":             "Select at least one split column",
	"请拖放有效的Excel文件（.xlsx或.xls格式）": "Drop a valid Excel file (.xlsx or .xls)",
	"读取表头失败: %v":                  "Failed to read the header: %v",
	"错误: 无法创建输出文件夹: %v":           "Error: could not create the output folder: %v",
	"错误: %v": "Error: %v",
	"完成":     "Completed",
	"✅ 拆分完成！\n\n分割列: %s\n总耗时: %s\n\n执行结果（总共 %d 条记录）:\n\n%s": "Split complete!\n\nColumns: %s\nElapsed: %s\n\nResults (%d files):\n\n%s",
	"%d分%.2f秒":     "%dm %.2fs",
	"%.2f秒":        "%.2fs",
	"检查更新时开发版本不支持": "Development builds cannot check for updates",
	"更新已回退":        "Update rolled back",
	"新版本 %s 启动失败，已自动回退到 %s。\n该版本将不再自动提示。": "Version %s failed to start and was rolled back to %s.\nThis version will not be offered again.",
	"开发版本不支持检查更新":         "Development builds cannot check for updates",
	"无法访问配置目录: %w":        "Cannot access the configuration directory: %w",
	"无法连接更新服务器: %w":       "Could not connect to the update server: %w",
	"当前已是最新版本 %s":         "You are running the latest version %s",
	"无法连接更新服务器":           "Could not connect to an update server",
	"当前版本 %s，最新版本 %s":     "Current version: %s; latest version: %s",
	"无法自动更新：%s":           "Automatic update unavailable: %s",
	"发现新版本 %s":            "Update available: %s",
	"稍后":                  "Later",
	"跳过此版本":               "Skip this version",
	"立即更新":                "Update now",
	"前往下载":                "Open download page",
	"请等待拆分完成后再更新":         "Wait for the split to finish before updating",
	"无法自动更新":              "Cannot update automatically",
	"%v\n\n是否打开下载页面手动下载？": "%v\n\nOpen the download page to download it manually?",
	"正在下载 %s":             "Downloading %s",
	"取消":                  "Cancel",
	"更新失败: %w":            "Update failed: %w",
	"更新完成":                "Update complete",
	"已更新到 %s，是否立即重启？\n选择“否”将在下次启动时生效。": "Updated to %s. Restart now?\nChoose No to apply it next time you start the app.",
	"重启失败，请手动重新打开程序: %w":               "Could not restart. Please reopen the app manually: %w",
	"觉得好用？微信扫码请作者喝杯咖啡，感谢支持！":           "Finding this useful? Scan with WeChat to support the author. Thank you!",
	"当前版本:":      "Current version:",
	"检查更新失败: %v": "Update check failed: %v",
	"已是最新版本":     "You are running the latest version",
	"发现新版本: %s":  "Update available: %s",
	"无法自动更新（%s），请手动下载: %s":                       "Cannot update automatically (%s). Download it manually: %s",
	"无法自动更新（%v），请手动下载: %s":                       "Cannot update automatically (%v). Download it manually: %s",
	"无法访问配置目录: %v":                               "Cannot access the configuration directory: %v",
	"下载中... %d%%":                                "Downloading... %d%%",
	"更新失败: %v":                                   "Update failed: %v",
	"已更新到 %s，重新运行程序即可生效":                         "Updated to %s. Restart the app to apply it.",
	"Excel闪电拆分工具 %s":                             "Excel Splitter %s",
	"使用方法: excel-splitter <输入文件> [输出文件夹]":        "Usage: excel-splitter <input-file> [output-folder]",
	"         excel-splitter --update   检查并安装更新": "         excel-splitter --update   Check for and install updates",
	"         excel-splitter --version  显示版本":    "         excel-splitter --version  Show version",
	"启动GUI界面并处理拖放的文件...":                         "Starting the GUI to process the dropped file...",
	"启动GUI界面...":                                 "Starting the GUI...",
	"关闭窗口":                                       "Close",
	"设置语言失败: %w":                                 "Could not save the language preference: %w",
	"、":                                          ", ",
	"更新源均不可用（Gitee: %v；GitHub: %w）":              "All update sources are unavailable (Gitee: %v; GitHub: %w)",
	"Gitee Release 缺少当前平台的安装包或校验文件":              "The Gitee release is missing this platform's package or checksum file",
	"查询最新版本失败: HTTP %d":                          "Could not check the latest version: HTTP %d",
	"解析版本信息失败: %w":                               "Could not parse release information: %w",
	"下载校验文件失败: %w":                               "Could not download the checksum file: %w",
	"校验文件中没有 %s":                                 "The checksum file does not contain %s",
	"下载更新失败: %w":                                 "Could not download the update: %w",
	"下载更新失败: HTTP %d":                            "Could not download the update: HTTP %d",
	"更新包过大: %d 字节":                               "The update package is too large: %d bytes",
	"更新包过大":                                      "The update package is too large",
	"文件校验失败，下载内容可能已损坏（期望 %s，实际 %s）": "Checksum verification failed; the download may be corrupted (expected %s, got %s)",
	"解压更新失败: %w":             "Could not extract the update: %w",
	"压缩包中没有 %s":              "The archive does not contain %s",
	"可执行文件过大":                "The executable is too large",
	"macOS 应用包暂不支持自动更新":      "Automatic updates are not supported for macOS app bundles yet",
	"程序所在目录没有写权限: %w":        "The application folder is not writable: %w",
	"备份当前版本失败: %w":           "Could not back up the current version: %w",
	"替换程序失败: %w":             "Could not replace the application: %w",
	"新版本文件不可用，可能被安全软件拦截: %w": "The new version is unavailable and may have been blocked by security software: %w",
	"回滚失败: %w":               "Rollback failed: %w",
	"当前版本: %s":               "Current version: %s",
	"打开文件失败: %w":             "Could not open the file: %w",
	"Excel文件中没有工作表":          "The Excel file contains no sheets",
	"读取工作表失败: %w":            "Could not read the worksheet: %w",
	"表头为空":                   "The header row is empty",
	"数据为空":                   "The worksheet has no data rows",
	"缺少分割列: %s":              "Missing split column: %s",
	"✗ %s: %s":               "✗ %s: %s",
	"该版本没有当前系统的安装包":          "This release has no package for the current system",
	"该版本缺少校验文件":              "This release has no checksum file",
	"该版本没有 %s 架构的安装包":        "This release has no package for the %s architecture",
	"大版本升级，请手动下载安装":          "Major-version updates must be downloaded manually",
}

func tr(text string) string {
	languageMu.RLock()
	lang := currentLanguage
	languageMu.RUnlock()
	if lang == languageEnglish {
		if translated, ok := englishText[text]; ok {
			return translated
		}
	}
	return text
}

func trf(text string, args ...any) string {
	return fmt.Sprintf(tr(text), args...)
}

func loadLanguagePreference() {
	statePath, err := updateStatePath()
	if err != nil {
		return
	}
	state, err := loadUpdateState(statePath)
	if err != nil {
		return
	}
	setLanguageValue(state.Language)
}

func setLanguageValue(lang string) {
	if lang != languageEnglish {
		lang = languageChinese
	}
	languageMu.Lock()
	currentLanguage = lang
	languageMu.Unlock()
}

func setLanguage(lang string) error {
	if lang != languageChinese && lang != languageEnglish {
		return errors.New("unsupported language")
	}
	setLanguageValue(lang)
	statePath, err := updateStatePath()
	if err != nil {
		return err
	}
	return modifyUpdateState(statePath, func(s *updateState) { s.Language = lang })
}

func showSettingsDialog(a fyne.App, w fyne.Window) {
	languageLabel := widget.NewLabel(tr("应用语言"))
	choice := widget.NewRadioGroup([]string{tr("简体中文"), "English"}, nil)
	if currentLanguage == languageEnglish {
		choice.SetSelected("English")
	} else {
		choice.SetSelected(tr("简体中文"))
	}
	d := dialog.NewCustom(tr("设置"), tr("关闭"), container.NewVBox(languageLabel, choice), w)
	d.Resize(fyne.NewSize(320, 150))
	choice.OnChanged = func(selected string) {
		lang := languageChinese
		if selected == "English" {
			lang = languageEnglish
		}
		if err := setLanguage(lang); err != nil {
			dialog.ShowError(fmt.Errorf(tr("设置语言失败: %w"), err), w)
		}
		if refreshLocalizedUI != nil {
			refreshLocalizedUI()
		}
		d.Hide()
		fyne.Do(func() { showSettingsDialog(a, w) })
	}
	d.Show()
}

func settingsMenu(a fyne.App, w fyne.Window) *fyne.MainMenu {
	return fyne.NewMainMenu(fyne.NewMenu(tr("设置"),
		fyne.NewMenuItem(tr("语言"), func() { showSettingsDialog(a, w) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(tr("打赏作者"), func() { showDonateDialog(w) }),
		fyne.NewMenuItem(tr("检查更新"), func() { checkForUpdates(a, w, true) }),
	))
}
