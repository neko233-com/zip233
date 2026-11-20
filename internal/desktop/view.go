package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

const sidebarWidth = float32(208)

// View builds the entire native window content.
func (a *App) View(c *ui.Context) {
	applyTheme(c)
	tk := theme.TokensOf(c)

	if a.toastSeq != a.toastShown {
		a.toastShown = a.toastSeq
		feedback.ShowToast(c, feedback.ToastProps{
			Type:        a.toastType,
			Title:       a.toastTitle,
			Description: a.toastDesc,
		})
	}

	width, _ := c.Size()
	root := ui.Column(c).Fill()
	if !c.Vibrancy() {
		root.Background(tk.BgLayout)
	}
	root.Children(func() {
		a.titleBar(c, tk)
		ui.Row(c).Grow(1).Basis(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.sidebar(c, tk)
			switch a.page {
			case pageSettings:
				layout.ScrollArea(c, layout.ScrollAreaProps{Fade: true}, func() {
					a.settingsPage(c, tk)
				}).Grow(1).Basis(0).MinHeight(0)
			case pageAbout:
				layout.ScrollArea(c, layout.ScrollAreaProps{Fade: true}, func() {
					a.aboutPage(c, tk)
				}).Grow(1).Basis(0).MinHeight(0)
			default:
				ui.Column(c).Grow(1).Basis(0).MinHeight(0).Children(func() {
					a.workbench(c, tk, width-sidebarWidth)
				})
			}
			a.createDrawer(c, tk)
			a.extractDrawer(c, tk)
			a.updateDrawer(c, tk)
			feedback.ToastHost(c, feedback.ToastHostProps{Placement: feedback.ToastBottomLeft})
		})
		a.statusBar(c, tk)
	})
}

func (a *App) titleBar(c *ui.Context, tk *theme.Tokens) {
	bar := c.TitleBar()
	height := float32(40)
	if bar.Height > height {
		height = bar.Height
	}
	ui.Row(c).Height(height).Shrink(0).Gap(9).AlignItems(ui.Center).
		Padding(0, bar.Right+16, 0, bar.Left+16).
		Background(tk.BgLayout).
		BorderWidth(0, 0, 1, 0).BorderColor(tk.BorderSecondary).
		DragWindow().Children(func() {
		if a.logo != nil {
			ui.Image(c, a.logo).Size(20, 20).Shrink(0)
		} else {
			ui.Box(c).Size(20, 20).Radius(4).Background(tk.Primary).Center().Children(func() {
				ui.Icon(c, lucide.Archive()).FontSize(12).TextColor(tk.PrimaryForeground)
			})
		}
		ui.Text(c, "zip233").FontSize(13).FontWeight(600).TextColor(tk.Text)
		ui.Spacer(c)
		ui.Text(c, versionText()).FontSize(11).TextColor(tk.TextQuaternary)
	})
}

func (a *App) sidebar(c *ui.Context, tk *theme.Tokens) {
	active := ""
	if a.page == pageWorkbench {
		active = a.filter
	}
	ui.Column(c).Width(sidebarWidth).Shrink(0).Padding(10, 8).Gap(4).
		Background(tk.BgContainer).
		BorderWidth(0, 1, 0, 0).BorderColor(tk.BorderSecondary).Children(func() {
		ui.Scroll(c).Grow(1).Basis(0).MinHeight(0).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				a.sidebarLabel(c, tk, "工作区")
				clicked := display.List(c, display.ListProps{
					Items: []display.ListItem{
						{Key: "all", Label: "全部", Icon: lucide.Inbox(), Extra: countText(len(a.archives))},
						{Key: "archives", Label: "归档文件", Icon: lucide.Archive(), Extra: countText(loadedCount(a.archives))},
						{Key: "creating", Label: "正在创建", Icon: lucide.LoaderCircle(), Extra: countText(taskCount(a.tasks, taskCreate, taskRunning))},
						{Key: "extracting", Label: "正在解压", Icon: lucide.PackageOpen(), Extra: countText(taskCount(a.tasks, taskExtract, taskRunning))},
						{Key: "failed", Label: "失败任务", Icon: lucide.CircleAlert(), Extra: countText(failedCount(a.tasks))},
					},
					Active:  active,
					Compact: true,
				})
				if clicked != "" {
					a.filter = clicked
					a.page = pageWorkbench
				}

				a.sidebarLabel(c, tk, "视图")
				clicked = display.List(c, display.ListProps{
					Items: []display.ListItem{
						{Key: "settings", Label: "设置", Icon: lucide.Settings2()},
						{Key: "about", Label: "关于", Icon: lucide.Info()},
					},
					Active:  pageKey(a.page),
					Compact: true,
				})
				switch clicked {
				case "settings":
					a.page = pageSettings
				case "about":
					a.page = pageAbout
				}
			})
		})

		ui.Box(c).Height(1).Background(tk.BorderSecondary).Margin(4, 0)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(4, 6).Children(func() {
			ui.Icon(c, lucide.Sparkles()).FontSize(11).TextColor(tk.TextQuaternary)
			ui.Text(c, "轻量的本地归档工具").FontSize(11).TextColor(tk.TextQuaternary).SingleLine()
		})
	})
}

func (a *App) sidebarLabel(c *ui.Context, tk *theme.Tokens, text string) {
	ui.Text(c, strings.ToUpper(text)).FontSize(10).FontWeight(600).LetterSpacing(0.4).
		TextColor(tk.TextQuaternary).Padding(6, 10, 2, 10)
}

func pageKey(p page) string {
	switch p {
	case pageSettings:
		return "settings"
	case pageAbout:
		return "about"
	}
	return ""
}

func countText(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func loadedCount(archives []ArchiveEntry) int {
	n := 0
	for _, entry := range archives {
		if entry.Items > 0 {
			n++
		}
	}
	return n
}

func taskCount(tasks []TaskEntry, kind taskKind, status taskStatus) int {
	n := 0
	for _, t := range tasks {
		if t.Kind == kind && t.Status == status {
			n++
		}
	}
	return n
}

func failedCount(tasks []TaskEntry) int {
	n := 0
	for _, t := range tasks {
		if t.Status == taskFailed {
			n++
		}
	}
	return n
}

func (a *App) statusBar(c *ui.Context, tk *theme.Tokens) {
	running := 0
	queued := 0
	done := 0
	failed := 0
	for _, t := range a.tasks {
		switch t.Status {
		case taskRunning:
			running++
		case taskQueued:
			queued++
		case taskDone:
			done++
		case taskFailed:
			failed++
		}
	}
	ui.Row(c).Height(32).Shrink(0).Gap(12).AlignItems(ui.Center).Padding(0, 14, 6, 14).
		Background(tk.BgContainer).BorderWidth(1, 0, 0, 0).BorderColor(tk.BorderSecondary).Children(func() {
		a.statusItem(c, tk, "归档", fmt.Sprint(len(a.archives)))
		a.statusItem(c, tk, "进行中", fmt.Sprint(running))
		if queued > 0 {
			a.statusItem(c, tk, "排队中", fmt.Sprint(queued))
		}
		a.statusItem(c, tk, "已完成", fmt.Sprint(done))
		if failed > 0 {
			a.statusItem(c, tk, "失败", fmt.Sprint(failed))
		}
		ui.Spacer(c)
		if text := a.statusUpdateText(); text != "" {
			b := ui.ButtonBase(c).Label(text).Row().Gap(4).Height(22).PaddingX(6).Radius(4)
			color := tk.TextTertiary
			switch a.updateState {
			case "available", "ready":
				color = tk.Primary
			case "error":
				color = tk.Error
			case "uptodate":
				color = tk.Success
			}
			b.Children(func() {
				if a.updateState == "downloading" || a.updateState == "checking" {
					ui.Icon(c, lucide.LoaderCircle()).FontSize(11).TextColor(color)
				}
				ui.Text(c, text).FontSize(11).LineHeight(1).TextColor(color).SingleLine().MaxWidth(170)
			})
			if b.Clicked() {
				a.updateOpen = true
				a.invalidate()
			}
		}
		ui.Text(c, "MyGo 原生界面").FontSize(11).TextColor(tk.TextQuaternary)
	})
}

func (a *App) statusItem(c *ui.Context, tk *theme.Tokens, label, value string) {
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11).LineHeight(1).TextColor(tk.TextQuaternary)
		ui.Text(c, value).FontSize(11).LineHeight(1).FontWeight(500).TextColor(tk.TextSecondary).FontFeatures("tnum")
	})
}

func (a *App) statusUpdateText() string {
	switch a.updateState {
	case "checking":
		return "检查更新中…"
	case "available":
		return "发现新版本"
	case "ready":
		return "更新已就绪"
	case "downloading":
		return fmt.Sprintf("下载更新 %d%%", int(a.updateProgress))
	case "uptodate":
		return "已是最新"
	case "error":
		return "更新检查失败"
	}
	return ""
}
