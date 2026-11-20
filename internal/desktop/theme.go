package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

// appTheme keeps Lobe's high-contrast near-black/near-white palette but uses
// Vercel/Geist style small radii and tight borders. It is built once for the
// life of the app, as theme.NewConfig retains each config it creates.
var appTheme = buildTheme()

func buildTheme() *theme.Config {
	light := theme.Default().Tokens(false)
	dark := theme.Default().Tokens(true)
	for _, tk := range []*theme.Tokens{light, dark} {
		tk.BorderRadius = 4
		tk.BorderRadiusLG = 6
		tk.BorderRadiusSM = 4
		tk.BorderRadiusXS = 2
	}
	return theme.NewConfig(*light, *dark)
}

func applyTheme(c *ui.Context) { appTheme.Apply(c) }
