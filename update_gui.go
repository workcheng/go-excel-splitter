package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// splitting 为 true 时正在拆分，此时不弹更新提示、不替换程序
var splitting atomic.Bool

// prepareUpdateOnLaunch 在创建窗口前调用：处理更新后的启动健康检查。
// 返回 false 表示已回滚到旧版本并重启，当前进程应直接退出。
func prepareUpdateOnLaunch() bool {
	statePath, err := updateStatePath()
	if err != nil {
		return true
	}
	exe, err := currentExecutable()
	if err != nil {
		return true
	}
	rolledBack, err := checkPendingUpdate(statePath, exe)
	if err != nil {
		updateLogf("启动检查失败: %v", err)
	}
	if rolledBack {
		if err := restartSelf(exe); err != nil {
			updateLogf("回滚后重启失败: %v", err)
		}
		return false
	}
	return true
}

// startUpdateTasks 在窗口显示后调用：确认本次启动正常，并在后台自动检查更新
func startUpdateTasks(a fyne.App, w fyne.Window) {
	statePath, err := updateStatePath()
	if err != nil {
		return
	}

	state, _ := loadUpdateState(statePath)
	if state.RolledBackFrom != "" {
		failed := state.RolledBackFrom
		modifyUpdateState(statePath, func(s *updateState) { s.RolledBackFrom = "" })
		dialog.ShowInformation("更新已回退",
			fmt.Sprintf("新版本 %s 启动失败，已自动回退到 %s。\n该版本将不再自动提示。", failed, version), w)
	}

	go func() {
		// 正常运行一段时间才算新版本启动成功
		time.Sleep(10 * time.Second)
		if exe, err := currentExecutable(); err == nil {
			if err := markUpdateHealthy(statePath, exe); err != nil {
				updateLogf("确认更新状态失败: %v", err)
			}
		}
	}()

	if version == "dev" || state.DisableAutoCheck || time.Since(state.LastCheck) < checkInterval {
		return
	}
	go func() {
		time.Sleep(5 * time.Second)
		checkForUpdates(a, w, false)
	}()
}

// checkForUpdates 检查更新。manual 为 true 时是用户点击按钮，需要给出所有结果的反馈；
// 自动检查则静默处理失败、没有更新和已跳过的版本。
func checkForUpdates(a fyne.App, w fyne.Window, manual bool) {
	if version == "dev" {
		if manual {
			dialog.ShowInformation("检查更新", "开发版本不支持检查更新", w)
		}
		return
	}
	statePath, err := updateStatePath()
	if err != nil {
		if manual {
			dialog.ShowError(fmt.Errorf("无法访问配置目录: %w", err), w)
		}
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		rel, err := checkLatest(ctx, version)
		cancel()
		if err != nil {
			updateLogf("检查更新失败: %v", err)
			if manual {
				fyne.Do(func() { dialog.ShowError(fmt.Errorf("无法连接更新服务器: %w", err), w) })
			}
			return
		}
		modifyUpdateState(statePath, func(s *updateState) { s.LastCheck = time.Now() })

		if rel == nil {
			if manual {
				fyne.Do(func() { dialog.ShowInformation("检查更新", "当前已是最新版本 "+version, w) })
			}
			return
		}
		if !manual {
			if state, _ := loadUpdateState(statePath); state.SkippedVersion == rel.Version {
				return
			}
			// 不打断正在进行的拆分
			for splitting.Load() {
				time.Sleep(2 * time.Second)
			}
		}
		fyne.Do(func() { showUpdateDialog(a, w, rel, statePath) })
	}()
}

func showUpdateDialog(a fyne.App, w fyne.Window, rel *releaseInfo, statePath string) {
	header := widget.NewLabel(fmt.Sprintf("当前版本 %s，最新版本 %s", version, rel.Version))
	notes := widget.NewRichTextFromMarkdown(rel.Notes)
	notes.Wrapping = fyne.TextWrapWord
	notesScroll := container.NewVScroll(notes)
	notesScroll.SetMinSize(fyne.NewSize(480, 240))
	top := container.NewVBox(header)
	if rel.Blocker != "" {
		top.Add(widget.NewLabel("无法自动更新：" + rel.Blocker))
	}

	d := dialog.NewCustomWithoutButtons("发现新版本 "+rel.Version, container.NewBorder(top, nil, nil, nil, notesScroll), w)
	laterBtn := widget.NewButton("稍后", d.Hide)
	skipBtn := widget.NewButton("跳过此版本", func() {
		modifyUpdateState(statePath, func(s *updateState) { s.SkippedVersion = rel.Version })
		d.Hide()
	})
	updateBtn := widget.NewButton("立即更新", func() {
		d.Hide()
		runUpdate(a, w, rel, statePath)
	})
	if rel.Blocker != "" {
		updateBtn.SetText("前往下载")
		updateBtn.OnTapped = func() {
			d.Hide()
			openReleasePage(a, rel)
		}
	}
	updateBtn.Importance = widget.HighImportance
	d.SetButtons([]fyne.CanvasObject{laterBtn, skipBtn, updateBtn})
	d.Show()
}

func openReleasePage(a fyne.App, rel *releaseInfo) {
	if u, err := url.Parse(rel.PageURL); err == nil {
		a.OpenURL(u)
	}
}

// runUpdate 下载、校验并替换程序，完成后询问是否立即重启
func runUpdate(a fyne.App, w fyne.Window, rel *releaseInfo, statePath string) {
	if splitting.Load() {
		dialog.ShowInformation("提示", "请等待拆分完成后再更新", w)
		return
	}
	exe, err := currentExecutable()
	if err == nil {
		err = checkCanReplace(exe)
	}
	if err != nil {
		dialog.ShowConfirm("无法自动更新", fmt.Sprintf("%v\n\n是否打开下载页面手动下载？", err), func(ok bool) {
			if ok {
				openReleasePage(a, rel)
			}
		}, w)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	bar := widget.NewProgressBar()
	progress := dialog.NewCustomWithoutButtons("正在下载 "+rel.Version, container.NewPadded(bar), w)
	progress.SetButtons([]fyne.CanvasObject{widget.NewButton("取消", cancel)})
	progress.Resize(fyne.NewSize(400, 140))
	progress.Show()

	state, _ := loadUpdateState(statePath)
	go func() {
		defer cancel()
		lastPercent := -1
		newPath, err := downloadUpdate(ctx, rel, exe, state.Mirror, func(done, total int64) {
			if total <= 0 {
				return
			}
			if p := int(done * 100 / total); p != lastPercent {
				lastPercent = p
				fyne.Do(func() { bar.SetValue(float64(p) / 100) })
			}
		})
		if err == nil {
			err = applyUpdate(statePath, exe, newPath, version, rel.Version)
		}

		fyne.Do(func() {
			progress.Hide()
			if errors.Is(err, context.Canceled) {
				return
			}
			if err != nil {
				updateLogf("更新失败: %v", err)
				dialog.ShowError(fmt.Errorf("更新失败: %w", err), w)
				return
			}
			dialog.ShowConfirm("更新完成", fmt.Sprintf("已更新到 %s，是否立即重启？\n选择“否”将在下次启动时生效。", rel.Version), func(ok bool) {
				if !ok {
					return
				}
				if err := restartSelf(exe); err != nil {
					dialog.ShowError(fmt.Errorf("重启失败，请手动重新打开程序: %w", err), w)
					return
				}
				a.Quit()
				os.Exit(0)
			}, w)
		})
	}()
}
