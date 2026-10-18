use reqwest::blocking::{Client, Response};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs::{self, File};
use std::io::{Read, Write};
use std::path::PathBuf;
use std::process::Command;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{mpsc, Arc, Mutex, OnceLock};
use std::time::Duration;

const RELEASE_API: &str = "https://api.github.com/repos/neko233-com/zip233/releases/latest";
const MIRRORS: &[&str] = &[
    "https://ghproxy.net/",
    "https://gh-proxy.com/",
    "https://ghfast.top/",
];

#[derive(Clone, Serialize)]
pub struct UpdateInfo {
    pub current_version: String,
    pub latest_version: Option<String>,
    pub available: bool,
    pub release_url: Option<String>,
    pub release_notes: Option<String>,
    pub asset_name: Option<String>,
    pub asset_size: Option<u64>,
    pub can_download: bool,
    pub message: String,
}

#[derive(Clone, Serialize)]
pub struct DownloadState {
    pub status: String,
    pub version: Option<String>,
    pub asset_name: Option<String>,
    pub downloaded_bytes: u64,
    pub total_bytes: Option<u64>,
    pub message: String,
    pub file_path: Option<String>,
}

impl DownloadState {
    fn idle() -> Self {
        Self {
            status: "idle".into(),
            version: None,
            asset_name: None,
            downloaded_bytes: 0,
            total_bytes: None,
            message: "尚未下载更新。".into(),
            file_path: None,
        }
    }
}

#[derive(Deserialize)]
struct GithubRelease {
    tag_name: String,
    html_url: String,
    body: Option<String>,
    assets: Vec<GithubAsset>,
}

#[derive(Deserialize)]
struct GithubAsset {
    name: String,
    browser_download_url: String,
    size: u64,
    digest: Option<String>,
}

#[derive(Clone)]
struct ValidatedAsset {
    version: String,
    name: String,
    download_url: String,
    size: u64,
    sha256: String,
}

static DOWNLOAD: OnceLock<Mutex<DownloadState>> = OnceLock::new();

fn download_state() -> &'static Mutex<DownloadState> {
    DOWNLOAD.get_or_init(|| Mutex::new(DownloadState::idle()))
}

pub fn check_for_updates() -> Result<UpdateInfo, String> {
    let current_version = env!("CARGO_PKG_VERSION").to_string();
    let release = match github_client()
        .get(RELEASE_API)
        .send()
        .map_err(|error| format!("连接 GitHub Release API 失败：{error}"))?
    {
        response if response.status() == reqwest::StatusCode::NOT_FOUND => {
            return Ok(UpdateInfo {
                current_version,
                latest_version: None,
                available: false,
                release_url: None,
                release_notes: None,
                asset_name: None,
                asset_size: None,
                can_download: false,
                message: "仓库还没有发布稳定版 Release。".into(),
            });
        }
        response => response
            .error_for_status()
            .and_then(|response| response.json::<GithubRelease>())
            .map_err(|error| format!("读取 GitHub Release 失败：{error}"))?,
    };

    let latest_version = normalize_version(&release.tag_name)?;
    let current = semver::Version::parse(&current_version)
        .map_err(|error| format!("当前 zip233 版本号无效：{error}"))?;
    let latest = semver::Version::parse(&latest_version)
        .map_err(|error| format!("GitHub Release 版本号无效：{error}"))?;
    let available = latest > current;
    let expected_asset = expected_asset_name(&latest_version);
    let asset = release
        .assets
        .iter()
        .find(|asset| asset.name == expected_asset);
    let can_download = available
        && asset.is_some_and(|asset| {
            asset
                .digest
                .as_deref()
                .is_some_and(|value| value.to_ascii_lowercase().starts_with("sha256:"))
        });
    let message = if !available {
        format!("当前已是最新版本（{current_version}）。")
    } else if asset.is_none() {
        format!("发现 {latest_version}，但 Release 没有当前平台安装包 {expected_asset}。")
    } else if !can_download {
        format!("发现 {latest_version}，但 GitHub 没有提供安装包 SHA-256 校验值，已禁用自动下载。")
    } else {
        format!("发现新版本 {latest_version}，安装包可下载并校验。")
    };

    Ok(UpdateInfo {
        current_version,
        latest_version: Some(latest_version),
        available,
        release_url: Some(release.html_url),
        release_notes: release.body.filter(|value| !value.trim().is_empty()),
        asset_name: asset.map(|asset| asset.name.clone()),
        asset_size: asset.map(|asset| asset.size),
        can_download,
        message,
    })
}

pub fn start_download() -> Result<DownloadState, String> {
    {
        let current = download_state()
            .lock()
            .map_err(|_| "更新状态锁已损坏。".to_string())?;
        if current.status == "downloading" {
            return Ok(current.clone());
        }
    }

    let asset = latest_validated_asset()?;
    let initial = DownloadState {
        status: "downloading".into(),
        version: Some(asset.version.clone()),
        asset_name: Some(asset.name.clone()),
        downloaded_bytes: 0,
        total_bytes: Some(asset.size),
        message: "正在连接 GitHub 下载更新…".into(),
        file_path: None,
    };
    *download_state()
        .lock()
        .map_err(|_| "更新状态锁已损坏。".to_string())? = initial.clone();

    std::thread::Builder::new()
        .name("zip233-update-download".into())
        .spawn(move || download_worker(asset))
        .map_err(|error| {
            set_download_error(format!("无法启动更新下载：{error}"));
            format!("无法启动更新下载：{error}")
        })?;
    Ok(initial)
}

pub fn current_download() -> DownloadState {
    download_state()
        .lock()
        .map(|state| state.clone())
        .unwrap_or_else(|_| DownloadState {
            status: "error".into(),
            message: "更新状态读取失败。".into(),
            ..DownloadState::idle()
        })
}

pub fn open_downloaded_update() -> Result<(), String> {
    let state = current_download();
    if state.status != "ready" {
        return Err("更新包尚未下载完成。".into());
    }
    let path = state
        .file_path
        .ok_or_else(|| "找不到已下载的更新包。".to_string())?;
    #[cfg(windows)]
    let mut command = {
        let mut command = Command::new("msiexec.exe");
        command.args(["/i", &path]);
        command
    };
    #[cfg(target_os = "macos")]
    let mut command = {
        let mut command = Command::new("open");
        command.arg(&path);
        command
    };
    command
        .spawn()
        .map(|_| ())
        .map_err(|error| format!("无法打开更新安装包：{error}"))
}

fn latest_validated_asset() -> Result<ValidatedAsset, String> {
    let release = github_client()
        .get(RELEASE_API)
        .send()
        .and_then(reqwest::blocking::Response::error_for_status)
        .and_then(|response| response.json::<GithubRelease>())
        .map_err(|error| format!("读取 GitHub Release 失败：{error}"))?;
    let version = normalize_version(&release.tag_name)?;
    let latest = semver::Version::parse(&version)
        .map_err(|error| format!("GitHub Release 版本号无效：{error}"))?;
    let current = semver::Version::parse(env!("CARGO_PKG_VERSION"))
        .map_err(|error| format!("当前 zip233 版本号无效：{error}"))?;
    if latest <= current {
        return Err(format!(
            "没有比当前版本更新的稳定版（当前 {current}，最新 {latest}）。"
        ));
    }
    let name = expected_asset_name(&version);
    let asset = release
        .assets
        .iter()
        .find(|asset| asset.name == name)
        .ok_or_else(|| format!("Release 中没有当前平台安装包 {name}。"))?;
    let sha256 = asset
        .digest
        .as_deref()
        .and_then(|value| {
            value
                .strip_prefix("sha256:")
                .or_else(|| value.strip_prefix("SHA256:"))
        })
        .filter(|value| value.len() == 64 && value.bytes().all(|byte| byte.is_ascii_hexdigit()))
        .ok_or_else(|| "GitHub 没有提供有效的 SHA-256，出于安全考虑已停止下载。".to_string())?
        .to_ascii_lowercase();
    Ok(ValidatedAsset {
        version,
        name: asset.name.clone(),
        download_url: asset.browser_download_url.clone(),
        size: asset.size,
        sha256,
    })
}

fn download_worker(asset: ValidatedAsset) {
    let base = std::env::temp_dir()
        .join("zip233")
        .join("updates")
        .join(&asset.version);
    if let Err(error) = fs::create_dir_all(&base) {
        set_download_error(format!("无法建立更新缓存目录：{error}"));
        return;
    }
    let final_path = base.join(&asset.name);
    let urls = std::iter::once(("GitHub 官方站点", asset.download_url.clone()))
        .chain(
            MIRRORS
                .iter()
                .map(|mirror| ("GitHub 加速镜像", format!("{mirror}{}", asset.download_url))),
        )
        .collect::<Vec<_>>();
    let cancelled = Arc::new(AtomicBool::new(false));
    let transferred = Arc::new(AtomicU64::new(0));
    let (sender, receiver) = mpsc::channel();
    let mut workers = Vec::new();
    let mut partial_paths = Vec::new();
    let mut failures = Vec::new();

    set_download_message("正在并行连接 GitHub 官方站点和加速镜像…".into());
    for (index, (label, url)) in urls.iter().enumerate() {
        let partial_path = base.join(format!("{}.{}.part", asset.name, index));
        partial_paths.push(partial_path.clone());
        let sender = sender.clone();
        let asset = asset.clone();
        let cancelled = Arc::clone(&cancelled);
        let transferred = Arc::clone(&transferred);
        let label = (*label).to_string();
        let worker_label = label.clone();
        let url = url.clone();
        match std::thread::Builder::new()
            .name(format!("zip233-update-source-{index}"))
            .spawn(move || {
                let result =
                    download_and_verify(&url, &partial_path, &asset, &cancelled, &transferred);
                if result.is_err() || cancelled.load(Ordering::Acquire) {
                    let _ = fs::remove_file(&partial_path);
                }
                let _ = sender.send((index, worker_label, partial_path, result));
            }) {
            Ok(worker) => workers.push(worker),
            Err(error) => failures.push(format!("{label}：无法启动下载：{error}")),
        }
    }
    drop(sender);

    let mut winner = None;
    for _ in 0..workers.len() {
        match receiver.recv() {
            Ok((index, label, path, Ok(()))) => {
                winner = Some((index, label, path));
                cancelled.store(true, Ordering::Release);
                break;
            }
            Ok((_, label, _, Err(error))) => failures.push(format!("{label}：{error}")),
            Err(error) => {
                failures.push(format!("下载线程意外退出：{error}"));
                break;
            }
        }
    }

    if let Some((_, winner_label, winner_path)) = winner {
        if let Err(error) = fs::rename(&winner_path, &final_path) {
            set_download_error(format!("更新包已校验，但无法保存：{error}"));
        } else {
            if let Ok(mut state) = download_state().lock() {
                state.status = "ready".into();
                state.downloaded_bytes = asset.size;
                state.total_bytes = Some(asset.size);
                state.message = format!(
                    "{} 下载完成，来源：{}，SHA-256 校验通过。",
                    asset.name, winner_label
                );
                state.file_path = Some(final_path.to_string_lossy().into_owned());
            }
        }

        // Stop losing transfers and remove their partial files when they finish.
        // Keeping this cleanup off the successful download path lets the user
        // install the verified winner immediately.
        let _ = std::thread::Builder::new()
            .name("zip233-update-cleanup".into())
            .spawn(move || {
                for worker in workers {
                    let _ = worker.join();
                }
                for path in partial_paths {
                    let _ = fs::remove_file(path);
                }
            });
        return;
    }

    cancelled.store(true, Ordering::Release);
    for worker in workers {
        let _ = worker.join();
    }
    for path in partial_paths {
        let _ = fs::remove_file(path);
    }
    set_download_error(format!("更新下载失败。{}", failures.join("；")));
}

fn download_and_verify(
    url: &str,
    target: &PathBuf,
    asset: &ValidatedAsset,
    cancelled: &AtomicBool,
    transferred: &AtomicU64,
) -> Result<(), String> {
    if cancelled.load(Ordering::Acquire) {
        return Err("已由更快的下载源替代。".into());
    }
    let response = download_client()
        .get(url)
        .send()
        .and_then(Response::error_for_status)
        .map_err(|error| error.to_string())?;
    let mut file = File::create(target).map_err(|error| error.to_string())?;
    let mut response = response;
    let mut hasher = Sha256::new();
    let mut downloaded = 0_u64;
    let mut buffer = [0_u8; 128 * 1024];
    loop {
        let count = response
            .read(&mut buffer)
            .map_err(|error| error.to_string())?;
        if count == 0 {
            break;
        }
        if cancelled.load(Ordering::Acquire) {
            return Err("已由更快的下载源替代。".into());
        }
        file.write_all(&buffer[..count])
            .map_err(|error| error.to_string())?;
        hasher.update(&buffer[..count]);
        downloaded = downloaded.saturating_add(count as u64);
        if downloaded > asset.size {
            return Err("下载内容超过 GitHub API 声明的安装包大小。".into());
        }
        transferred.fetch_max(downloaded, Ordering::Relaxed);
        if let Ok(mut state) = download_state().lock() {
            state.downloaded_bytes = transferred.load(Ordering::Relaxed).min(asset.size);
            state.total_bytes = Some(asset.size);
        }
    }
    file.flush().map_err(|error| error.to_string())?;
    file.sync_all().map_err(|error| error.to_string())?;
    let actual = format!("{:x}", hasher.finalize());
    if actual != asset.sha256 {
        return Err("下载内容的 SHA-256 与 GitHub 官方 Release API 不匹配。".into());
    }
    Ok(())
}

fn github_client() -> Client {
    Client::builder()
        .user_agent(concat!("zip233/", env!("CARGO_PKG_VERSION")))
        .connect_timeout(Duration::from_secs(12))
        .timeout(Duration::from_secs(900))
        .build()
        .expect("valid GitHub HTTP client configuration")
}

fn download_client() -> Client {
    Client::builder()
        .user_agent(concat!("zip233/", env!("CARGO_PKG_VERSION")))
        .connect_timeout(Duration::from_secs(12))
        .timeout(Duration::from_secs(240))
        .build()
        .expect("valid zip233 update download client configuration")
}

fn normalize_version(tag: &str) -> Result<String, String> {
    let normalized = tag.trim().trim_start_matches(['v', 'V']);
    semver::Version::parse(normalized)
        .map(|_| normalized.to_string())
        .map_err(|error| format!("无法识别 GitHub Release 标签 {tag:?}：{error}"))
}

fn expected_asset_name(version: &str) -> String {
    #[cfg(windows)]
    {
        format!("zip233-{version}-x64.msi")
    }
    #[cfg(target_os = "macos")]
    {
        format!("zip233-{version}.dmg")
    }
}

fn set_download_message(message: String) {
    if let Ok(mut state) = download_state().lock() {
        state.message = message;
        state.downloaded_bytes = 0;
    }
}

fn set_download_error(message: String) {
    if let Ok(mut state) = download_state().lock() {
        state.status = "error".into();
        state.message = message;
    }
}
