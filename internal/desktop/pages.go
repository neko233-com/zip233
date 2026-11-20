package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

func (a *App) settingsPage(c *ui.Context, tk *theme.Tokens) {
	ui.Row(c).FillWidth().Justify(ui.Center).Padding(24, 18, 36, 24).Children(func() {
		ui.Column(c).Width(760).MaxWidthPercent(100).Gap(16).Children(func() {
			a.pageHeader(c, tk, "设置", "配置右键菜单、更新行为和格式支持。")

			layout.Block(c, layout.BlockProps{Variant: layout.BlockFilled}).Padding(18).Gap(14).Children(func() {
				a.sectionTitle(c, tk, "资源管理器右键菜单")
				checkbox := choice.Checkbox(c, &a.shellEnabled, "加入资源管理器右键菜单", choice.CheckboxProps{})
				if checkbox.Changed() {
					a.setShellEnabled(a.shellEnabled)
				}
				status := a.refreshShellState()
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if status.Enabled && status.Healthy {
						ui.Icon(c, lucide.CircleCheck()).FontSize(15).TextColor(tk.Success)
						ui.Text(c, "已加入且运行正常").FontSize(12).TextColor(tk.Success)
					} else if a.shellEnabled {
						ui.Icon(c, lucide.CircleAlert()).FontSize(15).TextColor(tk.Warning)
						ui.Text(c, "已开启，正在注册或需要修复").FontSize(12).TextColor(tk.Warning)
					} else {
						ui.Icon(c, lucide.CircleAlert()).FontSize(15).TextColor(tk.TextTertiary)
						ui.Text(c, "已关闭").FontSize(12).TextColor(tk.TextSecondary)
					}
				})
				ui.Text(c, "开启后可在归档文件上右键使用“解压到同名文件夹”；在文件夹或文件夹空白处右键可压缩为 ZIP。").
					FontSize(12).LineHeight(1.6).TextColor(tk.TextSecondary)
				if a.shellEnabled && (!status.Enabled || !status.Healthy) {
					if action.Button(c, "立即修复", action.ButtonProps{Variant: action.Text, Size: theme.Small}).Clicked() {
						a.registerShell()
					}
				}
			})

			layout.Block(c, layout.BlockProps{Variant: layout.BlockFilled}).Padding(18).Gap(12).Children(func() {
				a.sectionTitle(c, tk, "更新")
				checkbox := choice.Checkbox(c, &a.autoCheck, "自动检查更新", choice.CheckboxProps{})
				if checkbox.Changed() {
					a.setAutoCheck(a.autoCheck)
				}
				ui.Text(c, "自动检查每 6 小时进行一次；检查到新版本只会下载安装包，不会自动安装。").
					FontSize(11).LineHeight(1.5).TextColor(tk.TextQuaternary).Margin(24, 0, 0, 0)

				ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "当前版本："+versionText()).FontSize(12).TextColor(tk.TextSecondary)
					if a.lastCheck.IsZero() {
						ui.Text(c, "尚未检查").FontSize(11).TextColor(tk.TextQuaternary)
					} else {
						ui.Text(c, "上次检查："+shortTime(a.lastCheck)).FontSize(11).TextColor(tk.TextQuaternary)
					}
				})
				if status := a.settingsUpdateStatus(); status != "" {
					ui.Text(c, status).FontSize(12).TextColor(a.updateStatusColor(tk))
				}
				if a.updateState == "downloading" {
					feedback.Progress(c, float32(a.updateProgress), feedback.ProgressProps{
						Size:     theme.Small,
						Status:   feedback.ProgressActive,
						ShowInfo: true,
					})
				}
				if a.updateErr != "" {
					ui.Text(c, a.updateErr).FontSize(11).TextColor(tk.Error).MaxLines(3)
				}
				ui.Row(c).Gap(8).Wrap().Children(func() {
					if action.Button(c, "检查更新", action.ButtonProps{Variant: action.Default, Size: theme.Small}).
						Disabled(a.updateChecking).Clicked() {
						a.checkUpdates(true)
					}
					switch a.updateState {
					case "available":
						if action.Button(c, "下载更新", action.ButtonProps{Variant: action.Primary, Size: theme.Small}).Clicked() {
							a.downloadUpdate()
						}
					case "ready":
						if action.Button(c, "安装并重启", action.ButtonProps{Variant: action.Primary, Size: theme.Small}).Clicked() {
							a.installUpdate()
						}
					case "downloading":
						if action.Button(c, "取消下载", action.ButtonProps{Variant: action.Default, Size: theme.Small}).Clicked() {
							a.cancelDownload()
						}
					}
					if action.Button(c, "安装本地更新包", action.ButtonProps{Variant: action.Text, Size: theme.Small}).Clicked() {
						a.installLocalUpdate()
					}
				})
				if a.updateInfo != nil && a.updateInfo.ReleaseURL != nil && *a.updateInfo.ReleaseURL != "" {
					ui.Text(c, "发布页："+*a.updateInfo.ReleaseURL).FontSize(11).TextColor(tk.TextQuaternary).SingleLine()
				}
			})

			layout.Block(c, layout.BlockProps{Variant: layout.BlockFilled}).Padding(18).Gap(10).Children(func() {
				a.sectionTitle(c, tk, "格式说明")
				ui.Text(c, "可创建："+strings.Join(createFormats, " / ")).FontSize(12).TextColor(tk.TextSecondary)
				ui.Text(c, "可读取：7z / zip / rar / tar / tar.gz / tar.xz / tar.zst / gz / bz2 / xz / zst").
					FontSize(12).TextColor(tk.TextSecondary)
				ui.Text(c, "密码保护仅支持 zip 与 7z；rar 等格式的写入需要系统安装 7-Zip 助手。").
					FontSize(11).LineHeight(1.6).TextColor(tk.TextQuaternary)
			})
		})
	})
}

func (a *App) aboutPage(c *ui.Context, tk *theme.Tokens) {
	ui.Row(c).FillWidth().Justify(ui.Center).Padding(28, 22, 36, 28).Children(func() {
		ui.Column(c).Width(700).MaxWidthPercent(100).Gap(16).Children(func() {
			a.pageHeader(c, tk, "关于 zip233", "一个轻量的本地归档工具。")
			layout.Block(c, layout.BlockProps{Variant: layout.BlockFilled}).Padding(22).Gap(14).Children(func() {
				ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
					if a.logo != nil {
						ui.Image(c, a.logo).Size(56, 56)
					}
					ui.Column(c).Gap(3).Children(func() {
						ui.Text(c, "zip233").FontSize(24).FontWeight(600).LetterSpacing(-0.4).TextColor(tk.TextHeading)
						ui.Text(c, versionText()).FontSize(12).TextColor(tk.TextTertiary)
					})
				})
				ui.Text(c, "zip233 使用 MyGo 原生 GPU 界面绘制，所有压缩、解压与测试都在本机完成，不上传任何文件。").
					FontSize(13).LineHeight(1.7).TextColor(tk.TextSecondary)
				ui.Text(c, "支持 Windows 资源管理器右键菜单集成。").FontSize(12).TextColor(tk.TextTertiary)
			})
			layout.Block(c, layout.BlockProps{Variant: layout.BlockFilled}).Padding(18).Gap(10).Children(func() {
				a.sectionTitle(c, tk, "支持的格式")
				ui.Row(c).Gap(6).Wrap().Children(func() {
					for _, label := range createFormats {
						display.Tag(c, strings.ToUpper(label), display.TagProps{
							Variant: display.TagFilled,
							Color:   display.TagGray,
							Size:    theme.Small,
							Round:   true,
						})
					}
				})
			})
			layout.Block(c, layout.BlockProps{Variant: layout.BlockFilled}).Padding(18).Gap(10).Children(func() {
				a.sectionTitle(c, tk, "技术")
				ui.Text(c, "界面：MyGo 原生 UI + Lobe MyGo 组件库 · 引擎：Go 标准库与 mholt/archives").
					FontSize(12).TextColor(tk.TextSecondary)
				if action.Button(c, "打开设置", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).Clicked() {
					a.page = pageSettings
					a.invalidate()
				}
			})
		})
	})
}

func (a *App) pageHeader(c *ui.Context, tk *theme.Tokens, title, subtitle string) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(24).FontWeight(600).LetterSpacing(-0.4).TextColor(tk.TextHeading)
		ui.Text(c, subtitle).FontSize(12).LineHeight(1.6).TextColor(tk.TextSecondary)
	})
}

func (a *App) sectionTitle(c *ui.Context, tk *theme.Tokens, title string) {
	ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(tk.Text)
}

func (a *App) settingsUpdateStatus() string {
	switch a.updateState {
	case "checking":
		return "正在检查更新…"
	case "available":
		return "发现新版本：" + latestVersion(a.updateInfo)
	case "downloading":
		return "正在下载更新…"
	case "ready":
		return "更新包已就绪，可以安装。"
	case "uptodate":
		return "已是最新版本。"
	case "error":
		return "更新检查失败。"
	}
	return ""
}

func (a *App) updateStatusColor(tk *theme.Tokens) ui.Color {
	switch a.updateState {
	case "available", "ready":
		return tk.Primary
	case "error":
		return tk.Error
	case "uptodate":
		return tk.Success
	default:
		return tk.TextSecondary
	}
}
