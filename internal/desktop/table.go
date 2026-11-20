package desktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/overlay"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

const (
	colFormat = float32(96)
	colSize   = float32(92)
	colItems  = float32(64)
	colState  = float32(104)
	colAdded  = float32(104)
)

func (a *App) workbench(c *ui.Context, tk *theme.Tokens, width float32) {
	ui.Column(c).Fill().Children(func() {
		a.toolbar(c, tk)
		a.table(c, tk, width)
		a.detailsStrip(c, tk)
	})
}

func (a *App) toolbar(c *ui.Context, tk *theme.Tokens) {
	ui.Row(c).Height(52).Shrink(0).Gap(8).AlignItems(ui.Center).PaddingX(14).
		Background(tk.BgLayout).BorderWidth(0, 0, 1, 0).BorderColor(tk.BorderSecondary).Children(func() {
		a.toolbarButton(c, tk, lucide.FolderOpen(), "打开归档", true, a.openArchive)
		a.toolbarButton(c, tk, lucide.FilePlus2(), "新建压缩包", true, func() {
			a.addOpen = true
		})
		ui.Box(c).Width(1).Height(20).Background(tk.BorderSecondary).Margin(2, 0)
		a.iconButton(c, lucide.ShieldCheck(), "测试选中归档", a.testArchive)
		a.iconButton(c, lucide.PackageOpen(), "解压选中归档", a.openExtractDrawer)
		a.iconButton(c, lucide.Trash2(), "清除已结束的任务", a.clearFinishedTasks)
		ui.Spacer(c)
		field, _ := searchField(c, &a.search, "搜索归档…")
		field.Width(220)
		if action.ActionIcon(c, lucide.Settings2(), action.ActionIconProps{
			Size:  theme.Middle,
			Title: "设置",
		}).Clicked() {
			a.page = pageSettings
			a.invalidate()
		}
	})
}

func (a *App) toolbarButton(c *ui.Context, tk *theme.Tokens, icon *ui.SVG, label string, primary bool, click func()) {
	b := ui.ButtonBase(c).Row().Gap(6).Height(30).PaddingX(10).Radius(4)
	variant := action.Default
	if primary {
		variant = action.Primary
	}
	action.StyleButton(c, b, "", action.ButtonProps{Variant: variant, Size: theme.Middle})
	b.Children(func() {
		ui.Icon(c, icon).FontSize(14)
		ui.Text(c, label).FontSize(12).FontWeight(500)
	})
	if b.Clicked() {
		click()
	}
}

func (a *App) iconButton(c *ui.Context, icon *ui.SVG, title string, click func()) {
	if action.ActionIcon(c, icon, action.ActionIconProps{
		Size:  theme.Middle,
		Title: title,
	}).Clicked() {
		click()
	}
}

func (a *App) table(c *ui.Context, tk *theme.Tokens, width float32) {
	archives := a.visibleArchives()
	ui.Column(c).Grow(1).Basis(0).MinHeight(0).Children(func() {
		a.tableHeader(c, tk, width)
		if len(archives) == 0 {
			ui.Box(c).Grow(1).Basis(0).Center().Children(func() {
				a.emptyState(c, tk)
			})
			return
		}
		layout.ScrollArea(c, layout.ScrollAreaProps{Fade: true}, func() {
			ui.Column(c).Children(func() {
				for _, entry := range archives {
					a.archiveRow(c, tk, width, entry)
				}
			})
		}).Grow(1).Basis(0).MinHeight(0)
	})
}

func (a *App) tableHeader(c *ui.Context, tk *theme.Tokens, width float32) {
	ui.Row(c).Height(32).Shrink(0).Gap(8).AlignItems(ui.Center).PaddingX(14).
		Background(tk.FillQuaternary).BorderWidth(0, 0, 1, 0).BorderColor(tk.BorderSecondary).Children(func() {
		b := ui.ButtonBase(c).Label("名称").Row().Gap(4).AlignItems(ui.Center).Justify(ui.Start).Height(24).PaddingX(4)
		b.Grow(1).Basis(0).MinWidth(0)
		b.Children(func() {
			ui.Text(c, "名称").FontSize(11).FontWeight(600).TextColor(tk.TextTertiary).SingleLine()
			if a.sortAsc {
				ui.Icon(c, lucide.ChevronUp()).FontSize(11).TextColor(tk.TextTertiary)
			} else {
				ui.Icon(c, lucide.ChevronDown()).FontSize(11).TextColor(tk.TextTertiary)
			}
		})
		if b.Clicked() {
			a.sortAsc = !a.sortAsc
			a.invalidate()
		}
		a.fixedHeader(c, tk, "格式", colFormat)
		a.fixedHeader(c, tk, "大小", colSize)
		a.fixedHeader(c, tk, "项目", colItems)
		a.fixedHeader(c, tk, "状态", colState)
		if width >= 900 {
			a.fixedHeader(c, tk, "添加时间", colAdded)
		}
	})
}

func (a *App) fixedHeader(c *ui.Context, tk *theme.Tokens, label string, width float32) {
	ui.Row(c).Width(width).Shrink(0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(tk.TextTertiary).SingleLine()
	})
}

func (a *App) archiveRow(c *ui.Context, tk *theme.Tokens, width float32, entry ArchiveEntry) {
	selected := a.selected == entry.ID
	row := ui.ButtonBase(c).Label(entry.Name).Row().Gap(8).AlignItems(ui.Center).
		Height(52).PaddingX(14).Shrink(0).
		BorderWidth(0, 0, 1, 0).BorderColor(tk.BorderSecondary).Cursor(ui.CursorPointer)
	switch {
	case selected:
		row.Background(tk.FillTertiary)
	case row.Hovered():
		row.Background(tk.FillQuaternary)
	}
	label, tagColor, running := a.statusOf(entry)
	row.Children(func() {
		ui.Box(c).Size(28, 28).Radius(6).Background(tk.FillTertiary).Center().Shrink(0).Children(func() {
			ui.Icon(c, lucide.FileArchive()).FontSize(14).TextColor(tk.TextSecondary)
		})
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, entry.Name).FontSize(13).FontWeight(500).TextColor(tk.Text).SingleLine()
			ui.Text(c, entry.Path).FontSize(11).TextColor(tk.TextTertiary).SingleLine()
		})
		ui.Text(c, strings.ToUpper(entry.Format)).Width(colFormat).Shrink(0).FontSize(12).TextColor(tk.TextSecondary).FontFeatures("tnum")
		ui.Text(c, humanBytes(entry.Size)).Width(colSize).Shrink(0).FontSize(12).TextColor(tk.TextSecondary).FontFeatures("tnum")
		ui.Text(c, fmt.Sprint(entry.Items)).Width(colItems).Shrink(0).FontSize(12).TextColor(tk.TextTertiary).FontFeatures("tnum")
		ui.Row(c).Width(colState).Shrink(0).AlignItems(ui.Center).Gap(4).Children(func() {
			if running {
				ui.Icon(c, lucide.LoaderCircle()).FontSize(11).TextColor(tk.Info)
			}
			display.Tag(c, label, display.TagProps{
				Variant: display.TagFilled,
				Color:   tagColor,
				Size:    theme.Small,
				Round:   true,
			})
		})
		if width >= 900 {
			ui.Text(c, shortTime(entry.Added)).Width(colAdded).Shrink(0).FontSize(11).TextColor(tk.TextQuaternary).FontFeatures("tnum")
		}
	})

	if row.Clicked() {
		a.selected = entry.ID
		a.invalidate()
	}
	if row.DoubleClicked() {
		a.reveal(entry.Path)
	}
	chosen := overlay.ContextMenu(c, row, overlay.DropdownMenuProps{Items: []overlay.MenuItem{
		{Key: "test", Label: "测试完整性", Icon: lucide.ShieldCheck()},
		{Key: "extract", Label: "解压…", Icon: lucide.PackageOpen()},
		{Key: "open", Label: "打开所在文件夹", Icon: lucide.FolderOpen()},
		{Key: "copy", Label: "复制路径", Icon: lucide.Copy()},
		{Divider: true},
		{Key: "remove", Label: "从列表移除", Icon: lucide.Trash2(), Danger: true},
	}})
	if chosen != "" {
		a.runArchiveAction(chosen, entry)
	}
}

func taskPercent(task TaskEntry) string {
	if task.Total <= 0 {
		// Streaming archives only learn their total during the walk. Showing
		// the processed count keeps the row informative without forcing an
		// extra full read of the archive just to compute a percentage.
		if task.Done > 0 {
			return fmt.Sprintf("%d 项", task.Done)
		}
		return ""
	}
	pct := float64(task.Done) / float64(task.Total) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%.0f%%", pct)
}

func (a *App) statusOf(entry ArchiveEntry) (string, display.TagColor, bool) {
	if task, ok := a.latestTask(entry.Path); ok {
		switch task.Status {
		case taskRunning:
			percent := taskPercent(task)
			switch task.Kind {
			case taskCreate:
				return "创建中 " + percent, display.TagProcessing, true
			case taskExtract:
				return "解压中 " + percent, display.TagProcessing, true
			default:
				return "测试中 " + percent, display.TagProcessing, true
			}
		case taskQueued:
			return "排队中", display.TagWarning, false
		case taskFailed:
			return "失败", display.TagError, false
		case taskDone:
			return "完成", display.TagSuccess, false
		}
	}
	if entry.Items > 0 {
		return "已就绪", display.TagSuccess, false
	}
	if entry.Note != "" {
		return "需密码", display.TagWarning, false
	}
	return "未读取", display.TagGray, false
}

func (a *App) runArchiveAction(key string, entry ArchiveEntry) {
	switch key {
	case "test":
		a.selected = entry.ID
		a.testArchive()
	case "extract":
		a.selected = entry.ID
		a.openExtractDrawer()
	case "open":
		a.reveal(entry.Path)
	case "copy":
		a.copyPath(entry.Path)
	case "remove":
		a.removeArchive(entry.ID)
	}
}

func (a *App) emptyState(c *ui.Context, tk *theme.Tokens) {
	display.Empty(c, display.EmptyProps{
		Icon:        lucide.Archive(),
		Title:       "还没有归档",
		Description: "打开一个归档文件，或新建压缩包开始使用。",
		Action: func() {
			if action.Button(c, "打开归档", action.ButtonProps{
				Variant: action.Primary,
				Size:    theme.Middle,
			}).Clicked() {
				a.openArchive()
			}
		},
	})
}

func (a *App) detailsStrip(c *ui.Context, tk *theme.Tokens) {
	entry, ok := a.selectedArchive()
	if !ok {
		return
	}
	label, tagColor, running := a.statusOf(entry)
	ui.Row(c).Height(72).Shrink(0).Gap(12).AlignItems(ui.Center).PaddingX(16).
		Background(tk.BgContainer).BorderWidth(1, 0, 0, 0).BorderColor(tk.BorderSecondary).Children(func() {
		ui.Box(c).Size(32, 32).Radius(6).Background(tk.FillTertiary).Center().Children(func() {
			ui.Icon(c, lucide.FileArchive()).FontSize(15).TextColor(tk.TextSecondary)
		})
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, entry.Name).FontSize(13).FontWeight(500).TextColor(tk.Text).SingleLine()
				display.Tag(c, label, display.TagProps{
					Variant: display.TagFilled,
					Color:   tagColor,
					Size:    theme.Small,
					Round:   true,
				})
				if running {
					ui.Icon(c, lucide.LoaderCircle()).FontSize(11).TextColor(tk.Info)
				}
			})
			meta := fmt.Sprintf("%s · %s · %d 个项目", strings.ToUpper(entry.Format), humanBytes(entry.Size), entry.Items)
			ui.Text(c, meta).FontSize(11).TextColor(tk.TextTertiary).SingleLine()
			if task, ok := a.latestTask(entry.Path); ok && task.Status == taskRunning {
				if task.Total > 0 && task.Done > 0 {
					pct := float32(float64(task.Done) / float64(task.Total) * 100)
					feedback.Progress(c, pct, feedback.ProgressProps{
						Status:   feedback.ProgressActive,
						Size:     theme.Small,
						ShowInfo: true,
						Label:    task.Message,
					}).Grow(1).Basis(0).MinWidth(80)
				} else {
					ui.Text(c, taskPercent(task)).FontSize(11).TextColor(tk.TextSecondary)
				}
			} else {
				ui.Text(c, entry.Path).FontSize(11).TextColor(tk.TextQuaternary).SingleLine()
			}
		})
		if action.Button(c, "测试", action.ButtonProps{Variant: action.Default, Size: theme.Small}).Clicked() {
			a.testArchive()
		}
		if action.Button(c, "解压", action.ButtonProps{Variant: action.Primary, Size: theme.Small}).Clicked() {
			a.openExtractDrawer()
		}
		if action.ActionIcon(c, lucide.FolderOpen(), action.ActionIconProps{Size: theme.Small, Title: "打开所在文件夹"}).Clicked() {
			a.reveal(entry.Path)
		}
		if action.ActionIcon(c, lucide.X(), action.ActionIconProps{Size: theme.Small, Title: "取消选择"}).Clicked() {
			a.selected = ""
			a.invalidate()
		}
	})
}

func shortTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("01-02 15:04")
}
