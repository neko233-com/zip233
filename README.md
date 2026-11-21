# zip233

![zip233](resources/icons/zip233-menu-256.png)

zip233 是 Windows / macOS 桌面归档管理器，**全部逻辑由 Go 1.27.3 编写**，不依赖 Node、Rust、cgo 或 Electron。归档读写、更新、右键菜单、安装器与打包工具均为 Go 实现；桌面界面使用 MyGo 原生 GPU UI 与 Lobe MyGo 组件绘制，无 WebView、无 HTML/JavaScript。CLI 在终端下使用 Bubble Tea 提供进度与结果卡片。

视觉采用 Vercel / Geist 风格：黑白灰高对比、三角形几何标识、细边框与点阵背景；桌面 UI、图标与 CLI 表现统一。同一枚 ▲ 三角形 logo 覆盖 GUI 窗口与托盘、CLI、MSI / NSIS / DMG 安装包、文档与资源管理器右键菜单，全平台保持一致。

核心原则：归档在本机处理；不要求用户安装 7-Zip；右键菜单仅写当前用户注册表；更新包必须校验 GitHub 官方 Release 提供的 SHA-256。

## 功能

- 打开、查看、测试、解压归档；创建 ZIP / TAR / TAR.GZ / TAR.BZ2 / TAR.XZ / GZIP / BZIP2 / XZ 归档。
- 7z 创建使用纯 Go `uned/sevenzip` 写入器，支持 LZMA2、solid 压缩、AES-256 加密与加密文件头；不再需要 7-Zip 助手。
- ZIP AES-256 加密创建与读取（WinZip AE-2：PBKDF2-HMAC-SHA1，内容 HMAC-SHA1；仅内容加密，ZIP 规范会公开文件名/目录；不支持 ZipCrypto 与 ZIP64）。7z / RAR 加密归档可读取。
- 读取 7z、ZIP、RAR、TAR、JAR 以及 ACE、ARJ、ARC/PAK、ZOO、LHA/LZH、UC2、SQ/SQZ、ICE、Pack-Ice 等历史格式；RAR 与历史格式为只读。历史格式的覆盖范围取决于底层格式库，不代表支持全部专有格式。
- CLI 提供 `bench` 基准测试与 `split` / `join` 分卷合并，便于对标 7-Zip 的日常工作流。
- 解压拒绝绝对路径、盘符、UNC 与 `..` 穿越，防止写出目标目录。
- Windows 资源管理器右键菜单：文件“使用 zip233 打开”，目录/背景“直接压缩为 ZIP”和“zip233 高级压缩…”，使用独立菜单图标；注册幂等并开机自修复。
- 多任务：桌面端最多同时运行 4 个归档任务（创建/解压/测试），其余排队，每个任务独立进度。CLI `bench --jobs N` 并行测多格式，默认 `min(CPU, 8)`。
- 软件更新：手动检查/下载/安装、自动检查与下载、可选自动安装、本地安装包更新；下载竞速官方源与加速镜像，强制 SHA-256 校验。

> 格式边界：7z 创建与加密为纯 Go 实现；`level=none` 使用非 solid 路径，其余使用 solid + LZMA2。ZIP AES-256 仅加密内容，文件名/目录仍可见；不支持 ZipCrypto 与 ZIP64。TAR 及其单流压缩格式不提供密码保护。历史格式不支持写入。CLI 分卷使用 `.zip233-split.json` 清单记录原始大小与 SHA-256，合并时会校验。

## 安装

### Windows

1. 直接下载 release 中的 `zip233-windows-amd64.msi`（稳定名）或 `zip233-<version>-x64.msi`（版本别名），双击安装。该 MSI 与 DownloadX 方案一致：WiX 3、per-machine 安装到 `C:\Program Files\zip233`、需要管理员权限、包含完整 UI 与 SHA-256 sidecar。
2. 或使用一键脚本：

```powershell
irm https://raw.githubusercontent.com/neko233-com/zip233/main/scripts/install.ps1 | iex
```

常用参数：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Version v0.2.2
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Silent
powershell -ExecutionPolicy Bypass -File .\install.ps1 -NoShellIntegration
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Uninstall
```

脚本会从 GitHub Release API 获取安装包与 SHA-256（GitHub asset digest 或 `<asset>.sha256` sidecar），校验失败立即中止；MSI 安装通过 `msiexec /i /passive` 提权执行，`-Silent` 使用 `/qn`。`-NoShellIntegration` 会在安装后关闭右键菜单注册。

本地构建 MSI：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-msi.ps1 -Version 0.2.2
```

需要 WiX 3.14（`candle.exe` / `light.exe`）或设置 `WIX_BIN`；输出 `zip233-windows-amd64.msi` 与同名 `.sha256`。

### macOS

```sh
curl -fsSL https://raw.githubusercontent.com/neko233-com/zip233/main/scripts/install.sh | sh
```

支持 `--version v0.2.2`、`--silent`、`--uninstall`、`--mirror <url>`、`--force`。macOS 安装器会把 `zip233.app` 放入 `/Applications`，无权限时回退到 `$HOME/Applications`。Linux 桌面版暂不支持，脚本会明确退出。

## 更新

- **手动更新**：设置 → 软件更新 → 检查更新 → 下载并校验 → 打开安装。
- **自动更新**：设置中开启“自动检查与下载”。启动后与每 6 小时检查一次；发现新版本自动下载并校验，安装前仍需确认（可选开启自动安装）。
- **本地包更新**：设置 → 软件更新 → 选择本地安装包。本地包不做官方摘要校验，请只使用可信来源。
- Windows 更新优先版本化 MSI，缺失时使用稳定名 MSI，再回退 EXE setup；macOS 使用 DMG。发布包同时提供 `.sha256`，API 未提供 digest 时更新器会读取 sidecar。

## Windows 右键菜单

设置 → 系统集成 → “加入资源管理器右键菜单”（默认开启，可随时关闭）。菜单项包括：

- 文件：使用 zip233 打开、解压到这里、解压到同名文件夹、解压到文件夹…、测试完整性
- 文件夹：直接压缩为 ZIP（静默生成，不覆盖同名文件）、zip233 高级压缩…
- 文件夹背景：直接压缩为 ZIP、zip233 高级压缩…

命令行直执行动作（右键静默压缩/解压/测试）不会打开窗口；加密归档在静默模式下会提示用 zip233 打开并输入密码。启动自修复与安装器写入同一组注册表项。

图标来自安装目录下的 `zip233-menu.ico`。重复注册只会更新 zip233 自有键值；启动时自动修复丢失或指向旧安装路径的注册。选择“移除”可清理所有 zip233 自有菜单项。

## 状态持久化

桌面 UI 在 `%LOCALAPPDATA%` 对应的用户配置目录（`os.UserConfigDir()/zip233/state.json`）保存归档列表、最近任务、默认输出目录与排序状态。密码不会明文持久化；重启后需要重新输入。未完成任务会标记为中断，避免误报成功。

## 图标

`resources/icons/` 包含全套 logo：16/24/32/48/64/128/256/512/1024 PNG、多分辨率 `zip233.ico`、macOS `zip233.icns`，以及右键菜单专用 `zip233-menu.ico` / PNG。由 Go 生成器从母版重建：

```sh
go run ./cmd/zip233-icons
```

## 开发

要求：Go 1.27.3+。Windows 打包使用 WiX 3.14（`candle.exe` / `light.exe`，可通过 `WIX_BIN` 指定）与 NSIS（`makensis`）；macOS 打包使用系统 `hdiutil`。

```sh
go run ./cmd/zip233              # MyGo 原生 GPU 桌面 UI
go run ./cmd/zip233-cli --help   # CLI（TTY 下 Bubble Tea，--json/非 TTY 为纯文本）
go test ./...
go vet ./...
go run ./cmd/zip233-icons        # 重建图标
go run ./cmd/zip233-pack         # 构建 MSI / EXE / DMG
```

CLI 示例：

```sh
zip233-cli formats --json
zip233-cli list archive.zip
zip233-cli test archive.7z --ask-password
zip233-cli extract archive.rar --output ./unpacked --password-stdin
zip233-cli create backup.7z ./project --format 7z --ask-password
zip233-cli bench --formats zip,7z,tar.gz --json
zip233-cli split backup.7z --size 50M
zip233-cli join backup.7z.part-001
```

密码不接受命令行明文，避免 shell 历史和进程列表泄露。`bench` 测试创建与解压速度、压缩比；`split` / `join` 提供分卷与 SHA-256 校验合并。

## 发布

默认在本机打包并发布 Release。`.github/workflows/release.yml` 仅保留手动触发：在 Actions 中选择 “Publish release”，输入已存在的标签（如 `v0.2.2`），才会构建 Windows MSI/EXE 与 macOS DMG 并发布资产。`build.yml` 与 `test.yml` 仍在 main 分支和 PR 上自动执行检查。
