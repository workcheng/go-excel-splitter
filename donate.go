package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

//go:embed donate_qr.png
var donateQRBytes []byte

var donateQR = fyne.NewStaticResource("donate_qr.png", donateQRBytes)

// showDonateDialog 显示微信收款码
func showDonateDialog(w fyne.Window) {
	img := canvas.NewImageFromResource(donateQR)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(260, 260))

	tip := widget.NewLabel("觉得好用？微信扫码请作者喝杯咖啡，感谢支持！")
	tip.Alignment = fyne.TextAlignCenter

	dialog.NewCustom("打赏作者", "关闭", container.NewVBox(img, tip), w).Show()
}
