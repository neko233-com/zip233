// Command zip233 是桌面 GUI 入口。
//
// 右键菜单的静默动作（compress-zip / compress-zip-here）在启动窗口之前
// 处理并立即退出；其余情况使用 MyGo 原生 GPU UI，不再启动本地 HTTP 服务
// 或浏览器窗口。web/ 保留作为未来的回退界面。
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/neko233-com/zip233/internal/desktop"
	"github.com/neko233-com/zip233/internal/service"
	"github.com/neko233-com/zip233/internal/version"
)

func main() {
	svc := service.New()

	// 右键菜单的直执行动作：静默完成压缩后立即退出，不打开 UI。
	req := svc.LaunchRequest()
	if req.Action == "compress-zip" || req.Action == "compress-zip-here" ||
		req.Action == "extract-here" || req.Action == "extract-to" || req.Action == "test-archive" {
		if req.Path == nil {
			fmt.Fprintln(os.Stderr, "操作失败: 缺少目标路径")
			os.Exit(1)
		}
		if err := svc.RunShellAction(req.Action, *req.Path); err != nil {
			fmt.Fprintf(os.Stderr, "操作失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	mygo.App.SetName("zip233")
	mygo.App.SetVersion(version.Version)

	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	app := desktop.New(svc)
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		if win := app.Window(); win != nil {
			win.Show()
			win.Focus()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		app.BeforeQuit()
	})

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:           "zip233",
			Width:           1180,
			Height:          790,
			MinWidth:        900,
			MinHeight:       600,
			TitleBarStyle:   mygo.TitleBarHidden,
			BackgroundColor: "light-dark(#ffffff, #0b0b0c)",
			Content:         ui.View(app.View),
		})
		_ = win.SetIcon(desktop.LogoPNG())
		app.Attach(win)
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
