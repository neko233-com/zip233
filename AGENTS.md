# AGENTS.md

## 基本约定

- 全部逻辑使用纯 Go；禁止引入 cgo、C/C++、Node、Rust、Electron 依赖。
- 桌面端使用 MyGo 原生 GPU UI，无 WebView；默认中文，同时支持中英文并跟随系统语言。
- 优先保证 Windows x64 与 macOS arm64；改动不得破坏另一平台。
- 维护 `.gitignore`：`target/`、本地二进制、日志与构建缓存不得入库。

## 提交前验证

- `go build ./...`、`go vet ./...`、`go test -count=1 ./...` 全部通过。
- 依赖变更后 `go mod tidy -diff` 必须无输出。
- 改动 Go 文件后保持 `gofmt` 格式。

## MSI 打包与本机安装（Windows 强制）

- 在本机 Windows 打包 MSI 后，必须立即用本次产物升级安装到最新版本，不能只构建不安装。
- 安装命令（管理员终端）：

```powershell
msiexec /i target\release\zip233-windows-amd64.msi /passive /norestart
```

- 安装后必须校验：
  - `C:\Program Files\zip233\zip233.exe` 与 `zip233-cli.exe` 存在；
  - `"C:\Program Files\zip233\zip233-cli.exe" --version` 输出与标签版本一致；
  - 旧版本已被 MajorUpgrade 替换，系统中不存在双份安装。
- 发布资产同时提供稳定名与版本名，并为每个安装包生成 `.sha256` sidecar。

## 更新能力（必须保留）

- 手动更新：设置或托盘“检查更新” → 下载并校验 → “安装并重启”，另保留本地安装包更新入口。
- 自动更新：启动时与每 6 小时自动检查并下载；安装前必须由用户确认；开关持久化到 `os.UserConfigDir()/zip233/update.json`。
- 更新只走 GitHub 官方 Release，下载竞速官方源与镜像；SHA-256 校验失败立即中止。
- 改动更新逻辑后运行 `go test ./internal/update ./internal/service`，并确认手动与自动两条链路仍可用。

## 发布

- 默认在本机打包并发布；Actions 的 `Publish release` 仅手动触发，需输入已存在的标签。
- 三平台 CI 全绿后才允许打标签。
- 发布后核验 Release 资产齐全，`.sha256` 内容与 GitHub asset digest 一致。
