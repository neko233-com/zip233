package desktop

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

// createFormats are the formats the archive engine can write.
var createFormats = []string{"zip", "7z", "tar", "tar.gz", "tar.xz", "tar.bz2", "gz", "bz2", "xz"}

func (a *App) createDrawer(c *ui.Context, tk *theme.Tokens) {
	feedback.Drawer(c, &a.addOpen, feedback.DrawerProps{
		Placement:     feedback.Right,
		Size:          460,
		Title:         "新建压缩包",
		NoCloseButton: true,
		Footer: func() {
			if action.Button(c, "取消", action.ButtonProps{
				Variant: action.Text,
				Size:    theme.Middle,
			}).Clicked() {
				a.addOpen = false
			}
			if action.Button(c, "开始压缩", action.ButtonProps{
				Variant: action.Primary,
				Size:    theme.Middle,
			}).Clicked() {
				a.startCreate()
			}
		},
	}, func() {
		ui.Column(c).Gap(14).Children(func() {
			ui.Text(c, "选择要压缩的文件或文件夹，然后指定保存位置与格式。").
				FontSize(12).LineHeight(1.6).TextColor(tk.TextSecondary)

			ui.Column(c).Gap(6).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "要压缩的内容").FontSize(12).TextColor(tk.TextTertiary)
					ui.Spacer(c)
					ui.Text(c, strconv.Itoa(len(a.sources))+" 项").FontSize(11).TextColor(tk.TextQuaternary)
				})
				if len(a.sources) == 0 {
					ui.Row(c).Height(40).AlignItems(ui.Center).PaddingX(10).
						Radius(tk.BorderRadius).Border(1, tk.BorderSecondary).Children(func() {
						ui.Text(c, "尚未添加任何文件").FontSize(12).TextColor(tk.TextPlaceholder)
					})
				} else {
					remove := -1
					for i, src := range a.sources {
						source, index := src, i
						ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).PaddingX(8).
							BorderWidth(0, 0, 1, 0).BorderColor(tk.BorderSecondary).Children(func() {
							ui.Icon(c, lucide.FileText()).FontSize(13).TextColor(tk.TextTertiary).Shrink(0)
							ui.Text(c, source).FontSize(11).TextColor(tk.TextSecondary).SingleLine().Grow(1).Basis(0).MinWidth(0)
							if action.ActionIcon(c, lucide.X(), action.ActionIconProps{
								Size:  theme.Small,
								Title: "移除",
							}).Clicked() {
								remove = index
							}
						})
					}
					if remove >= 0 && remove < len(a.sources) {
						a.sources = append(a.sources[:remove], a.sources[remove+1:]...)
					}
				}
				ui.Row(c).Gap(8).Children(func() {
					if action.Button(c, "添加文件", action.ButtonProps{Variant: action.Default, Size: theme.Small}).Clicked() {
						a.addSourceFiles()
					}
					if action.Button(c, "添加文件夹", action.ButtonProps{Variant: action.Default, Size: theme.Small}).Clicked() {
						a.addSourceFolder()
					}
				})
			})

			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "保存为").FontSize(12).TextColor(tk.TextTertiary)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					target, _ := textField(c, tk, &a.target, "选择压缩包保存位置", lucide.FileArchive(), 34, 13, false)
					target.Grow(1).Basis(0)
					if action.ActionIcon(c, lucide.FolderOpen(), action.ActionIconProps{
						Variant: action.ActionIconBorderless,
						Size:    theme.Middle,
						Title:   "浏览",
					}).Clicked() {
						a.chooseTarget()
					}
				})
			})

			ui.Row(c).Gap(12).Children(func() {
				ui.Column(c).Grow(1).Basis(0).Gap(6).Children(func() {
					ui.Text(c, "格式").FontSize(12).TextColor(tk.TextTertiary)
					field.Select(c, &a.format, field.SelectProps{Options: formatOptions(), Size: theme.Middle})
				})
				ui.Column(c).Width(120).Gap(6).Children(func() {
					ui.Text(c, "压缩级别").FontSize(12).TextColor(tk.TextTertiary)
					field.Select(c, &a.level, field.SelectProps{Options: levelOptions(), Size: theme.Middle})
				})
			})

			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "密码（可选）").FontSize(12).TextColor(tk.TextTertiary)
				password, _ := textField(c, tk, &a.password, "仅 zip / 7z 支持密码", lucide.KeyRound(), 34, 13, false)
				password.Grow(1).Basis(0)
			})

			ui.Text(c, "密码保护仅对 zip 与 7z 生效；tar 系列格式会忽略该选项。").
				FontSize(11).LineHeight(1.6).TextColor(tk.TextQuaternary)
		})
	})
}

func (a *App) extractDrawer(c *ui.Context, tk *theme.Tokens) {
	feedback.Drawer(c, &a.extractOpen, feedback.DrawerProps{
		Placement:     feedback.Right,
		Size:          440,
		Title:         "解压归档",
		NoCloseButton: true,
		Footer: func() {
			if action.Button(c, "取消", action.ButtonProps{
				Variant: action.Text,
				Size:    theme.Middle,
			}).Clicked() {
				a.extractOpen = false
			}
			if action.Button(c, "开始解压", action.ButtonProps{
				Variant: action.Primary,
				Size:    theme.Middle,
			}).Clicked() {
				a.startExtract()
			}
		},
	}, func() {
		ui.Column(c).Gap(16).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "归档文件").FontSize(12).TextColor(tk.TextTertiary)
				ui.Row(c).Height(34).AlignItems(ui.Center).PaddingX(10).Gap(8).
					Radius(tk.BorderRadius).Border(1, tk.BorderSecondary).Background(tk.BgContainer).Children(func() {
					ui.Icon(c, lucide.FileArchive()).FontSize(13).TextColor(tk.TextTertiary).Shrink(0)
					ui.Text(c, a.extractPath).FontSize(12).TextColor(tk.TextSecondary).SingleLine().Grow(1).Basis(0).MinWidth(0)
				})
			})

			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "解压到").FontSize(12).TextColor(tk.TextTertiary)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					output, _ := textField(c, tk, &a.outputDir, "选择目标文件夹", lucide.Folder(), 34, 13, false)
					output.Grow(1).Basis(0)
					if action.ActionIcon(c, lucide.FolderOpen(), action.ActionIconProps{
						Variant: action.ActionIconBorderless,
						Size:    theme.Middle,
						Title:   "浏览",
					}).Clicked() {
						a.chooseOutputDir()
					}
				})
			})

			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "密码（可选）").FontSize(12).TextColor(tk.TextTertiary)
				password, _ := textField(c, tk, &a.extractPassword, "加密归档需要填写密码", lucide.KeyRound(), 34, 13, false)
				password.Grow(1).Basis(0)
			})

			ui.Text(c, "解压会在后台执行，完成后会弹出提示。").FontSize(11).TextColor(tk.TextQuaternary)
		})
	})
}

func (a *App) updateDrawer(c *ui.Context, tk *theme.Tokens) {
	feedback.Drawer(c, &a.updateOpen, feedback.DrawerProps{
		Placement:     feedback.Right,
		Size:          500,
		Title:         "检查更新",
		NoCloseButton: true,
		Footer: func() {
			if action.Button(c, "关闭", action.ButtonProps{
				Variant: action.Text,
				Size:    theme.Middle,
			}).Clicked() {
				a.updateOpen = false
			}
			switch a.updateState {
			case "available":
				if action.Button(c, "下载更新", action.ButtonProps{
					Variant: action.Primary,
					Size:    theme.Middle,
				}).Clicked() {
					a.downloadUpdate()
				}
			case "ready":
				if action.Button(c, "安装并重启", action.ButtonProps{
					Variant: action.Primary,
					Size:    theme.Middle,
				}).Clicked() {
					a.installUpdate()
				}
			case "downloading":
				if action.Button(c, "取消下载", action.ButtonProps{
					Variant: action.Default,
					Size:    theme.Middle,
				}).Clicked() {
					a.cancelDownload()
				}
			default:
				if action.Button(c, "立即检查", action.ButtonProps{
					Variant: action.Primary,
					Size:    theme.Middle,
				}).Clicked() {
					a.checkUpdates(true)
				}
			}
		},
	}, func() {
		ui.Column(c).Gap(14).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "当前版本："+versionText()).FontSize(12).TextColor(tk.TextSecondary)
				if a.updateInfo != nil && latestVersion(a.updateInfo) != "" {
					ui.Text(c, "→ v"+latestVersion(a.updateInfo)).FontSize(12).FontWeight(600).TextColor(tk.Primary)
				}
			})
			switch a.updateState {
			case "downloading":
				feedback.Progress(c, float32(a.updateProgress), feedback.ProgressProps{
					Size:     theme.Small,
					Status:   feedback.ProgressActive,
					ShowInfo: true,
				})
			case "uptodate":
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, lucide.CircleCheck()).FontSize(18).TextColor(tk.Success)
					ui.Text(c, "已是最新版本").FontSize(13).TextColor(tk.TextSecondary)
				})
			case "error":
				ui.Text(c, a.updateErr).FontSize(12).TextColor(tk.Error).MaxLines(4)
			case "available":
				ui.Text(c, "发现新版本，点击右下角按钮下载安装包。").FontSize(12).TextColor(tk.TextSecondary)
			case "ready":
				ui.Text(c, "安装包已下载并校验完成，可以安装。").FontSize(12).TextColor(tk.TextSecondary)
			default:
				ui.Text(c, "可以从 GitHub Releases 检查最新版本。").FontSize(12).LineHeight(1.6).TextColor(tk.TextTertiary)
			}
			if a.updateInfo != nil && a.updateInfo.ReleaseNotes != nil && *a.updateInfo.ReleaseNotes != "" {
				ui.Text(c, *a.updateInfo.ReleaseNotes).FontSize(12).LineHeight(1.7).TextColor(tk.TextSecondary).MaxLines(8)
			}
		})
	})
}

// searchField builds a compact search input with a search icon.
func searchField(c *ui.Context, value *string, placeholder string) (ui.Element, ui.Element) {
	root := ui.Row(c).Height(32).AlignItems(ui.Center)
	var input ui.Element
	root.Children(func() {
		f, in := textField(c, theme.TokensOf(c), value, placeholder, lucide.Search(), 32, 12, false)
		f.Grow(1).Basis(0)
		input = in
	})
	return root, input
}

// textField builds a token-based text input that can grow inside a row.
func textField(c *ui.Context, tk *theme.Tokens, value *string, placeholder string, icon *ui.SVG, height, fontSize float32, autoFocus bool) (ui.Element, ui.Element) {
	root := ui.Row(c).Gap(8).AlignItems(ui.Center).Height(height).Padding(0, 10).
		Radius(tk.BorderRadius).Border(1, tk.BorderSecondary).Background(tk.BgContainer)
	var input ui.Element
	root.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(fontSize).TextColor(tk.TextTertiary).Shrink(0)
		}
		input = ui.TextInputBase(c, value).Grow(1).FocusRing(false).
			Placeholder(placeholder).PlaceholderColor(tk.TextPlaceholder).FontSize(fontSize).TextColor(tk.Text)
		if autoFocus {
			input.AutoFocus()
		}
	})
	if input.Valid() && input.Focused() {
		root.Border(1, tk.Primary)
	} else if root.Hovered() {
		root.Border(1, tk.Border)
	}
	return root, input
}

func formatOptions() []field.SelectOption {
	options := make([]field.SelectOption, 0, len(createFormats))
	for _, format := range createFormats {
		options = append(options, field.SelectOption{Value: format, Label: strings.ToUpper(format)})
	}
	return options
}

func levelOptions() []field.SelectOption {
	return []field.SelectOption{
		{Value: "", Label: "默认"},
		{Value: "none", Label: "仅存储"},
		{Value: "fast", Label: "最快"},
		{Value: "best", Label: "最好"},
	}
}
