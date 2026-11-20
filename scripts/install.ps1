<#
.SYNOPSIS
    zip233 Windows 一键安装 / 更新 / 卸载脚本。

.DESCRIPTION
    从 GitHub Release 获取安装包与 SHA-256 摘要，校验通过后安装。
    优先使用稳定资产名 zip233-windows-amd64.msi，其次版本化 MSI，最后回退 NSIS setup.exe。
    MSI 安装到 C:\Program Files\zip233，需要管理员权限（通过 -Verb RunAs 提权）。

.EXAMPLE
    irm https://raw.githubusercontent.com/neko233-com/zip233/main/install.ps1 | iex
    $env:ZIP233_VERSION = "0.2.2"; irm .../install.ps1 | iex
    powershell -ExecutionPolicy Bypass -File install.ps1 -Version v0.2.2 -Silent
    powershell -ExecutionPolicy Bypass -File install.ps1 -Uninstall -Silent
#>
param(
    [string]$Version = "latest",
    [switch]$Uninstall,
    [switch]$NoShellIntegration,
    [switch]$Silent,
    [string]$Mirror,
    [switch]$Force
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = "neko233-com/zip233"
$Headers = @{
    "User-Agent" = "zip233-installer"
    "Accept"     = "application/vnd.github+json"
}
$StableMsiName = "zip233-windows-amd64.msi"

function Write-Step {
    param([string]$Message)
    if (-not $Silent) { Write-Host "▲ zip233 $Message" -ForegroundColor Cyan }
}

function Write-WarnMsg {
    param([string]$Message)
    Write-Host "▲ zip233 警告: $Message" -ForegroundColor Yellow
}

function Write-ErrMsg {
    param([string]$Message)
    Write-Host "▲ zip233 错误: $Message" -ForegroundColor Red
}

function Get-ReleaseInfo {
    param([string]$RequestedVersion)

    if ([string]::IsNullOrWhiteSpace($RequestedVersion) -or $RequestedVersion -eq "latest") {
        $url = "https://api.github.com/repos/$Repo/releases/latest"
    } else {
        $tag = $RequestedVersion
        if (-not $tag.StartsWith("v")) { $tag = "v$tag" }
        $url = "https://api.github.com/repos/$Repo/releases/tags/$tag"
    }

    Write-Step "正在查询 Release 信息: $url"
    try {
        return Invoke-RestMethod -Uri $url -Headers $Headers -TimeoutSec 30
    } catch {
        throw "获取 GitHub Release 信息失败: $($_.Exception.Message)"
    }
}

function Find-ReleaseAsset {
    param($Release, [string]$Tag)

    $ver = $Tag.TrimStart("v")
    $assets = @($Release.assets)

    # 1) Stable, re-published asset name (what irm|iex expects).
    $msi = $assets | Where-Object { $_.name -eq $StableMsiName } | Select-Object -First 1
    # 2) Versioned alias used by the in-app updater.
    if (-not $msi) {
        $msi = $assets | Where-Object { $_.name -eq "zip233-$ver-x64.msi" } | Select-Object -First 1
    }
    # 3) Any x64 MSI.
    if (-not $msi) {
        $msi = $assets | Where-Object { $_.name -like "*.msi" -and $_.name -like "*x64*" } | Select-Object -First 1
    }
    if ($msi) { return @{ Asset = $msi; Kind = "msi" } }

    $exe = $assets | Where-Object { $_.name -eq "zip233-windows-amd64-setup.exe" } | Select-Object -First 1
    if (-not $exe) {
        $exe = $assets | Where-Object { $_.name -eq "zip233-$ver-x64-setup.exe" } | Select-Object -First 1
    }
    if (-not $exe) {
        $exe = $assets | Where-Object { $_.name -like "*setup*.exe" } | Select-Object -First 1
    }
    if (-not $exe) {
        $exe = $assets | Where-Object { $_.name -like "*.exe" } | Select-Object -First 1
    }
    if ($exe) { return @{ Asset = $exe; Kind = "exe" } }

    return $null
}

function Get-AssetDigest {
    param($Asset, $Release)

    # GitHub's official digest field is preferred.
    if ($Asset.digest) {
        $digest = [string]$Asset.digest
        if ($digest -match "(?i)^sha256:") { $digest = $digest.Substring(7) }
        if ($digest -match "(?i)^[0-9a-f]{64}$") { return $digest.ToLowerInvariant() }
    }

    # Then a per-asset SHA-256 sidecar, then the stable MSI's sidecar.
    $digestNames = @("$($Asset.name).sha256")
    if ($Asset.name -ne $StableMsiName) { $digestNames += "$StableMsiName.sha256" }
    foreach ($digestName in $digestNames) {
        $digestAsset = @($Release.assets) | Where-Object { $_.name -eq $digestName } | Select-Object -First 1
        if ($digestAsset) {
            try {
                $text = (Invoke-WebRequest -Uri $digestAsset.browser_download_url -UseBasicParsing -TimeoutSec 30).Content
                if ($text -match "(?i)([0-9a-f]{64})") { return $Matches[1].ToLowerInvariant() }
            } catch {
                Write-WarnMsg "读取摘要文件 $digestName 失败: $($_.Exception.Message)"
            }
        }
    }
    return $null
}

function Download-Asset {
    param(
        [string]$OfficialUrl,
        [string]$Tag,
        [string]$Name,
        [string]$Destination,
        [string]$MirrorUrl
    )

    $urls = @()
    if (-not [string]::IsNullOrWhiteSpace($MirrorUrl)) {
        $urls += "$($MirrorUrl.TrimEnd('/'))/$Tag/$Name"
    }
    $urls += $OfficialUrl

    $lastError = $null
    foreach ($url in $urls) {
        try {
            Write-Step "正在下载: $url"
            Invoke-WebRequest -Uri $url -OutFile $Destination -UseBasicParsing -TimeoutSec 300
            return
        } catch {
            $lastError = $_
            Write-WarnMsg "下载失败，尝试下一个源: $($_.Exception.Message)"
            if (Test-Path $Destination) {
                Remove-Item $Destination -Force -ErrorAction SilentlyContinue
            }
        }
    }
    throw "所有下载源均失败: $($lastError.Exception.Message)"
}

function Verify-File {
    param([string]$Path, [string]$ExpectedDigest)

    $actual = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLowerInvariant()
    if ($actual -ne $ExpectedDigest.ToLowerInvariant()) {
        Remove-Item $Path -Force -ErrorAction SilentlyContinue
        throw "SHA-256 校验失败：期望 $ExpectedDigest，实际 $actual。已中止安装。"
    }
    Write-Step "SHA-256 校验通过"
}

function Install-Package {
    param([string]$Path, [string]$Kind)

    if ($Kind -eq "msi") {
        # perMachine MSI -> always elevate; /passive shows progress, /qn is silent.
        $msiArgs = @("/i", "`"$Path`"", "/norestart")
        if ($Silent) { $msiArgs += "/qn" } else { $msiArgs += "/passive" }
        Write-Step "开始安装 MSI（需要管理员权限）$(if ($Silent) { '（静默）' })"
        $proc = Start-Process -FilePath "msiexec.exe" -ArgumentList $msiArgs -Verb RunAs -Wait -PassThru
        if ($proc.ExitCode -notin @(0, 3010)) {
            throw "msiexec 安装失败，退出码 $($proc.ExitCode)"
        }
        if ($proc.ExitCode -eq 3010) { Write-WarnMsg "安装成功，但需要重启后生效。" }
    } else {
        $setupArgs = @()
        if ($Silent) { $setupArgs += "/S" }
        Write-Step "开始安装 setup.exe$(if ($Silent) { '（静默）' })"
        $proc = Start-Process -FilePath $Path -ArgumentList $setupArgs -Wait -PassThru
        if ($proc.ExitCode -ne 0) {
            throw "setup.exe 安装失败，退出码 $($proc.ExitCode)"
        }
    }
}

function Disable-ShellIntegration {
    $key = "HKCU:\Software\neko233\zip233"
    if (-not (Test-Path $key)) {
        New-Item -Path $key -Force | Out-Null
    }
    New-ItemProperty -Path $key -Name "ShellIntegrationEnabled" -PropertyType DWord -Value 0 -Force | Out-Null
    Write-Step "已写入 ShellIntegrationEnabled=0（关闭右键菜单集成）"
}

function Enable-ShellIntegration {
    # The perMachine MSI is elevated, so its HKCU flag may land in the admin
    # profile. Set the flag for the real current user; the runtime self-repairs
    # the actual Explorer verbs on first launch.
    $key = "HKCU:\Software\neko233\zip233"
    if (-not (Test-Path $key)) {
        New-Item -Path $key -Force | Out-Null
    }
    if ($null -eq (Get-ItemProperty -Path $key -Name "ShellIntegrationEnabled" -ErrorAction SilentlyContinue)) {
        New-ItemProperty -Path $key -Name "ShellIntegrationEnabled" -PropertyType DWord -Value 1 -Force | Out-Null
        Write-Step "已启用右键菜单集成（运行时将自修复注册表项）"
    }
}

function Uninstall-Zip233 {
    Write-Step "正在查找已安装的 zip233 ..."

    $roots = @(
        "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*",
        "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*",
        "HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*"
    )
    $productCodes = @()
    foreach ($root in $roots) {
        Get-ItemProperty -Path $root -ErrorAction SilentlyContinue |
            Where-Object { $_.DisplayName -like "zip233*" } |
            ForEach-Object {
                if ($_.PSChildName -match "^\{") { $productCodes += $_.PSChildName }
            }
    }

    $productCodes = $productCodes | Select-Object -Unique
    if ($productCodes.Count -gt 0) {
        foreach ($code in $productCodes) {
            Write-Step "通过 msiexec /x $code 卸载（需要管理员权限）"
            $msiArgs = @("/x", $code, "/norestart")
            if ($Silent) { $msiArgs += "/qn" } else { $msiArgs += "/passive" }
            $proc = Start-Process -FilePath "msiexec.exe" -ArgumentList $msiArgs -Verb RunAs -Wait -PassThru
            if ($proc.ExitCode -notin @(0, 3010)) {
                Write-WarnMsg "卸载 $code 返回退出码 $($proc.ExitCode)"
            }
        }
        return
    }

    $uninstaller = Join-Path $env:LOCALAPPDATA "Programs\zip233\uninstall.exe"
    if (Test-Path $uninstaller) {
        Write-Step "执行本地卸载程序: $uninstaller"
        $setupArgs = @()
        if ($Silent) { $setupArgs += "/S" }
        $proc = Start-Process -FilePath $uninstaller -ArgumentList $setupArgs -Wait -PassThru
        if ($proc.ExitCode -ne 0) {
            throw "卸载程序失败，退出码 $($proc.ExitCode)"
        }
        return
    }

    Write-WarnMsg "未找到已安装的 zip233，无需卸载。"
}

# ---------------------------------------------------------------- main flow
if ($env:ZIP233_VERSION -and $Version -eq "latest") {
    $Version = $env:ZIP233_VERSION
}
Write-Step "zip233 一键安装程序启动"

try {
    if ($Uninstall) {
        Uninstall-Zip233
        Write-Step "卸载流程结束"
        exit 0
    }

    $release = Get-ReleaseInfo -RequestedVersion $Version
    $tag = [string]$release.tag_name
    if ([string]::IsNullOrWhiteSpace($tag)) {
        throw "Release 信息缺少 tag_name，无法继续"
    }
    Write-Step "目标版本: $tag"

    $selected = Find-ReleaseAsset -Release $release -Tag $tag
    if (-not $selected) {
        throw "Release $tag 中既找不到 MSI 也找不到 setup.exe"
    }
    $asset = $selected.Asset
    Write-Step "选择安装包: $($asset.name)（$($selected.Kind)）"

    $digest = Get-AssetDigest -Asset $asset -Release $release
    if (-not $digest) {
        throw "无法获取 $($asset.name) 的 SHA-256 摘要，出于安全考虑已中止"
    }

    $cacheDir = Join-Path $env:TEMP "zip233-downloads"
    if (-not (Test-Path $cacheDir)) {
        New-Item -ItemType Directory -Path $cacheDir -Force | Out-Null
    }
    $destination = Join-Path $cacheDir $asset.name

    if ((Test-Path $destination) -and -not $Force) {
        Write-Step "使用已缓存的安装包: $destination"
    } else {
        Download-Asset -OfficialUrl $asset.browser_download_url -Tag $tag -Name $asset.name `
            -Destination $destination -MirrorUrl $Mirror
    }

    Verify-File -Path $destination -ExpectedDigest $digest
    Install-Package -Path $destination -Kind $selected.Kind

    if ($NoShellIntegration) {
        Disable-ShellIntegration
    } else {
        Enable-ShellIntegration
    }

    Write-Host ""
    Write-Step "安装完成。"
    Write-Step "主程序: C:\Program Files\zip233\zip233.exe（开始菜单: zip233）"
    Write-Step "命令行: C:\Program Files\zip233\zip233-cli.exe"
    Write-Step "缓存包: $destination"
} catch {
    Write-ErrMsg $_.Exception.Message
    exit 1
}
