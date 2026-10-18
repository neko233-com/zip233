#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod embedded {
    include!(concat!(env!("OUT_DIR"), "/zip233_assets.rs"));
}

use rdesktop_core::config::{AppConfig, WindowConfig};
use rdesktop_core::ipc::{FnIpcHandler, IpcMessage, IpcResponse};
use rdesktop_core::renderer::Renderer;
use rdesktop_webview::WebViewRenderer;
use serde::Serialize;
use serde_json::{json, Value};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::OnceLock;

#[derive(Serialize)]
struct ToolResult {
    ok: bool,
    code: Option<i32>,
    stdout: String,
    stderr: String,
}

#[derive(Serialize)]
struct ArchiveItem {
    name: String,
    size: Option<u64>,
    packed_size: Option<u64>,
    is_directory: bool,
    modified: Option<String>,
}

#[derive(Serialize)]
struct LaunchRequest {
    action: String,
    path: Option<String>,
}

#[derive(Serialize)]
struct ShellIntegrationStatus {
    enabled: bool,
    healthy: bool,
}

static LAUNCH_REQUEST: OnceLock<Option<LaunchRequest>> = OnceLock::new();

fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();
    ensure_engine().map_err(anyhow::Error::msg)?;
    repair_shell_integration_on_startup();

    let mut config = AppConfig {
        identifier: "com.neko233.zip233".into(),
        name: "zip233".into(),
        version: env!("CARGO_PKG_VERSION").into(),
        window: WindowConfig {
            title: "zip233".into(),
            width: 1180,
            height: 790,
            min_size: Some((860, 600)),
            icon: load_window_icon(),
            ..Default::default()
        },
        ..Default::default()
    };
    config.renderer.trusted_origins = vec!["rdesktop://localhost".into()];

    let mut renderer = WebViewRenderer::new(&config)?;
    renderer.init()?;
    renderer.set_embedded_assets(embedded::embedded_assets())?;
    renderer.set_ipc_handler(Box::new(FnIpcHandler::new(handle_ipc)));
    #[cfg(feature = "native-agent")]
    renderer.enable_native_agent_from_env()?;

    let window = renderer.create_window(&config.window)?;
    #[cfg(debug_assertions)]
    if let Ok(url) = std::env::var("RDESKTOP_DEV_URL") {
        renderer.load_url(window, &url)?;
        return Box::new(renderer).run().map_err(Into::into);
    }
    renderer.load_url(window, "rdesktop://localhost/index.html")?;
    Box::new(renderer).run()?;
    Ok(())
}

fn handle_ipc(message: IpcMessage) -> IpcResponse {
    let id = message.id;
    let result = dispatch_ipc(&message.cmd, &message.payload);
    match result {
        Ok(data) => IpcResponse { id, success: true, data },
        Err(error) => IpcResponse {
            id,
            success: false,
            data: json!({ "error": error }),
        },
    }
}

fn dispatch_ipc(command: &str, payload: &Value) -> Result<Value, String> {
    match command {
        "pick_archive" => {
            let path = rfd::FileDialog::new()
                .set_title("打开归档")
                .add_filter("归档与磁盘映像", ARCHIVE_EXTENSIONS)
                .pick_file();
            Ok(path.map(|value| value.to_string_lossy().into_owned()).into())
        }
        "pick_sources" => {
            let paths = rfd::FileDialog::new()
                .set_title("选择要压缩的文件")
                .pick_files()
                .unwrap_or_default()
                .into_iter()
                .map(|value| value.to_string_lossy().into_owned())
                .collect::<Vec<_>>();
            Ok(json!(paths))
        }
        "pick_source_folder" => {
            let path = rfd::FileDialog::new()
                .set_title("选择要压缩的文件夹")
                .pick_folder();
            Ok(path.map(|value| value.to_string_lossy().into_owned()).into())
        }
        "pick_output_folder" => {
            let path = rfd::FileDialog::new()
                .set_title("选择解压目标文件夹")
                .pick_folder();
            Ok(path.map(|value| value.to_string_lossy().into_owned()).into())
        }
        "save_archive" => {
            let extension = payload
                .get("extension")
                .and_then(Value::as_str)
                .filter(|value| supported_create_format(value))
                .ok_or_else(|| "不支持的归档格式。".to_string())?;
            let default_name = payload
                .get("defaultName")
                .and_then(Value::as_str)
                .unwrap_or("归档文件");
            let path = rfd::FileDialog::new()
                .set_title("保存归档文件")
                .set_file_name(default_name)
                .add_filter("归档文件", &[extension])
                .save_file();
            Ok(path.map(|value| value.to_string_lossy().into_owned()).into())
        }
        "list_archive" => {
            let path = required_string(payload, "path")?;
            let result = run_tool(list_args(path, optional_string(payload, "password")))?;
            ensure_success(&result)?;
            Ok(serde_json::to_value(parse_archive_items(&result.stdout)).map_err(|e| e.to_string())?)
        }
        "test_archive" => {
            let path = required_string(payload, "path")?;
            let result = run_tool(test_args(path, optional_string(payload, "password")))?;
            serde_json::to_value(result).map_err(|e| e.to_string())
        }
        "extract_archive" => {
            let path = required_string(payload, "path")?;
            let output_dir = required_string(payload, "outputDir")?;
            let result = extract_archive(path, output_dir, optional_string(payload, "password"))?;
            serde_json::to_value(result).map_err(|e| e.to_string())
        }
        "create_archive" => {
            let paths = payload
                .get("paths")
                .and_then(Value::as_array)
                .ok_or_else(|| "请选择要压缩的文件或文件夹。".to_string())?
                .iter()
                .map(|value| value.as_str().map(str::to_string).ok_or_else(|| "文件路径无效。".to_string()))
                .collect::<Result<Vec<_>, _>>()?;
            let target = required_string(payload, "target")?;
            let format = required_string(payload, "format")?;
            let level = payload.get("level").and_then(Value::as_str).unwrap_or("5");
            let password = optional_string(payload, "password");
            let result = create_archive(&paths, target, format, level, password)?;
            serde_json::to_value(result).map_err(|e| e.to_string())
        }
        "get_launch_request" => {
            let request = LAUNCH_REQUEST.get_or_init(parse_launch_request);
            serde_json::to_value(request).map_err(|e| e.to_string())
        }
        "shell_integration_status" => {
            serde_json::to_value(shell_integration_status()).map_err(|e| e.to_string())
        }
        "register_shell_integration" | "repair_shell_integration" => {
            register_shell_integration()?;
            serde_json::to_value(shell_integration_status()).map_err(|e| e.to_string())
        }
        "unregister_shell_integration" => {
            unregister_shell_integration()?;
            serde_json::to_value(shell_integration_status()).map_err(|e| e.to_string())
        }
        _ => Err(format!("未知的 zip233 操作：{command}")),
    }
}

const ARCHIVE_EXTENSIONS: &[&str] = &[
    "7z", "zip", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz", "zst",
    "lz", "lzma", "cab", "wim", "iso", "dmg", "rpm", "deb", "cpio", "arj", "lzh", "chm",
    "vhd", "vhdx", "vmdk", "vdi", "qcow2", "udf", "xar", "z", "apfs", "squashfs",
    "cramfs", "fat", "ntfs", "gpt", "mbr", "uefi", "ihex", "nsis", "msi", "hfs",
];

fn required_string<'a>(payload: &'a Value, name: &str) -> Result<&'a str, String> {
    payload
        .get(name)
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| format!("缺少有效参数：{name}"))
}

fn optional_string<'a>(payload: &'a Value, name: &str) -> Option<&'a str> {
    payload.get(name).and_then(Value::as_str).filter(|value| !value.is_empty())
}

fn password_args(password: Option<&str>) -> Vec<String> {
    password.map(|value| vec![format!("-p{value}")]).unwrap_or_default()
}

fn list_args(path: &str, password: Option<&str>) -> Vec<String> {
    let mut args = vec!["l".into(), "-slt".into(), "-sccUTF-8".into()];
    args.extend(password_args(password));
    args.push("--".into());
    args.push(path.into());
    args
}

fn test_args(path: &str, password: Option<&str>) -> Vec<String> {
    let mut args = vec!["t".into(), "-sccUTF-8".into()];
    args.extend(password_args(password));
    args.push("--".into());
    args.push(path.into());
    args
}

fn parse_archive_items(output: &str) -> Vec<ArchiveItem> {
    let mut items = Vec::new();
    let mut fields = BTreeMap::<String, String>::new();
    let mut saw_archive_header = false;

    fn flush(
        fields: &mut BTreeMap<String, String>,
        items: &mut Vec<ArchiveItem>,
        saw_archive_header: &mut bool,
    ) {
        if fields.is_empty() {
            return;
        }
        let path = fields.get("Path").cloned().unwrap_or_default();
        if !path.is_empty() && *saw_archive_header {
            let is_directory = fields.get("Folder").map(String::as_str) == Some("+")
                || fields.get("Attributes").is_some_and(|value| value.contains('D'));
            items.push(ArchiveItem {
                name: path,
                size: fields.get("Size").and_then(|value| value.parse().ok()),
                packed_size: fields.get("Packed Size").and_then(|value| value.parse().ok()),
                is_directory,
                modified: fields.get("Modified").cloned(),
            });
        } else if fields.contains_key("Type") {
            *saw_archive_header = true;
        }
        fields.clear();
    }

    for line in output.lines() {
        if line.trim().is_empty() {
            flush(&mut fields, &mut items, &mut saw_archive_header);
        } else if let Some((key, value)) = line.split_once(" = ") {
            fields.insert(key.trim().into(), value.trim().into());
        }
    }
    flush(&mut fields, &mut items, &mut saw_archive_header);
    items
}

fn extract_archive(path: &str, output_dir: &str, password: Option<&str>) -> Result<ToolResult, String> {
    let mut args = vec!["x".into(), "-sccUTF-8".into(), format!("-o{output_dir}"), "-y".into()];
    args.extend(password_args(password));
    if wrapped_tar_kind(path).is_none() {
        args.push("--".into());
        args.push(path.into());
        return run_tool(args);
    }

    let temporary = tempfile::Builder::new()
        .prefix("zip233-")
        .tempdir()
        .map_err(|error| format!("无法创建临时归档目录：{error}"))?;
    let intermediate_tar = temporary.path().join("contents.tar");
    let mut decompress = vec!["x".into(), "-so".into(), "-sccUTF-8".into()];
    decompress.extend(password_args(password));
    decompress.push("--".into());
    decompress.push(path.into());
    let decompressed = run_tool_to_file(decompress, &intermediate_tar)?;
    ensure_success(&decompressed)?;

    let mut tar_args = vec!["x".into(), "-sccUTF-8".into(), format!("-o{output_dir}"), "-y".into()];
    tar_args.push("--".into());
    tar_args.push(intermediate_tar.to_string_lossy().into_owned());
    run_tool(tar_args)
}

fn wrapped_tar_kind(path: &str) -> Option<&'static str> {
    let name = path.to_ascii_lowercase();
    if name.ends_with(".tar.gz") || name.ends_with(".tgz") {
        Some("gzip")
    } else if name.ends_with(".tar.xz") || name.ends_with(".txz") {
        Some("xz")
    } else if name.ends_with(".tar.bz2") || name.ends_with(".tbz2") || name.ends_with(".tbz") {
        Some("bzip2")
    } else {
        None
    }
}

fn create_archive(
    paths: &[String],
    target: &str,
    format: &str,
    level: &str,
    password: Option<&str>,
) -> Result<ToolResult, String> {
    if paths.is_empty() {
        return Err("先选择至少一个文件或文件夹。".into());
    }
    if !supported_create_format(format) {
        return Err(format!("不支持创建 {format} 格式。"));
    }
    if !matches!(level, "1" | "5" | "7" | "9") {
        return Err("压缩等级无效。".into());
    }
    if matches!(format, "gzip" | "bzip2" | "xz") && paths.len() != 1 {
        return Err("GZIP、BZIP2 和 XZ 是单文件压缩格式，请只选择一个文件；压缩多个文件请选 TAR.GZ、TAR.BZ2 或 TAR.XZ。".into());
    }

    if matches!(format, "tar.gz" | "tar.xz" | "tar.bz2") {
        let temporary = tempfile::Builder::new()
            .prefix("zip233-")
            .tempdir()
            .map_err(|error| format!("无法创建临时归档目录：{error}"))?;
        let target_name = Path::new(target)
            .file_name()
            .and_then(|name| name.to_str())
            .unwrap_or("archive");
        let tar_name = target_name
            .rsplit_once('.')
            .map(|(stem, _)| format!("{stem}.tar"))
            .unwrap_or_else(|| format!("{target_name}.tar"));
        let tar_path = temporary.path().join(tar_name);
        let mut tar_args = vec!["a".into(), "-ttar".into(), "-mx=0".into(), "-sccUTF-8".into(), "-y".into()];
        tar_args.push(tar_path.to_string_lossy().into_owned());
        tar_args.push("--".into());
        tar_args.extend(paths.iter().cloned());
        let created_tar = run_tool(tar_args)?;
        ensure_success(&created_tar)?;

        let compressor = match format {
            "tar.gz" => "gzip",
            "tar.xz" => "xz",
            _ => "bzip2",
        };
        let mut args = vec!["a".into(), format!("-t{compressor}"), format!("-mx={level}"), "-sccUTF-8".into(), "-y".into()];
        args.push(target.into());
        args.push(tar_path.to_string_lossy().into_owned());
        return run_tool(args);
    }

    let mut args = vec!["a".into(), format!("-t{format}"), format!("-mx={level}"), "-sccUTF-8".into(), "-y".into()];
    if format == "7z" && password.is_some() {
        args.push("-mhe=on".into());
    }
    args.push(target.into());
    args.extend(password_args(password));
    args.push("--".into());
    args.extend(paths.iter().cloned());
    run_tool(args)
}

fn supported_create_format(format: &str) -> bool {
    matches!(format, "7z" | "zip" | "tar" | "tar.gz" | "tar.xz" | "tar.bz2" | "gzip" | "bzip2" | "xz" | "wim")
}

fn load_window_icon() -> Option<rdesktop_core::WindowIcon> {
    let image = image::load_from_memory(include_bytes!("../resources/icons/zip233-256.png")).ok()?.into_rgba8();
    let (width, height) = image.dimensions();
    Some(rdesktop_core::WindowIcon {
        rgba: image.into_raw(),
        width,
        height,
    })
}

fn run_tool(args: Vec<String>) -> Result<ToolResult, String> {
    let engine = ensure_engine()?;
    let mut command = Command::new(engine.exe);
    command.args(args).stdout(Stdio::piped()).stderr(Stdio::piped());
    hide_console(&mut command);
    let output = command.output().map_err(|error| format!("无法启动归档引擎：{error}"))?;
    Ok(ToolResult {
        ok: output.status.success(),
        code: output.status.code(),
        stdout: String::from_utf8_lossy(&output.stdout).into_owned(),
        stderr: String::from_utf8_lossy(&output.stderr).into_owned(),
    })
}

fn run_tool_to_file(args: Vec<String>, output_path: &Path) -> Result<ToolResult, String> {
    let engine = ensure_engine()?;
    let output = std::fs::File::create(output_path).map_err(|error| format!("无法创建临时文件：{error}"))?;
    let mut command = Command::new(engine.exe);
    command.args(args).stdout(Stdio::from(output)).stderr(Stdio::piped());
    hide_console(&mut command);
    let result = command.output().map_err(|error| format!("无法启动归档引擎：{error}"))?;
    Ok(ToolResult {
        ok: result.status.success(),
        code: result.status.code(),
        stdout: String::new(),
        stderr: String::from_utf8_lossy(&result.stderr).into_owned(),
    })
}

#[cfg(windows)]
fn hide_console(command: &mut Command) {
    use std::os::windows::process::CommandExt;
    command.creation_flags(0x08000000);
}

#[cfg(not(windows))]
fn hide_console(_command: &mut Command) {}

fn ensure_success(result: &ToolResult) -> Result<(), String> {
    if result.ok {
        Ok(())
    } else {
        Err(preferred_error(result))
    }
}

fn preferred_error(result: &ToolResult) -> String {
    result
        .stderr
        .lines()
        .rev()
        .find(|line| !line.trim().is_empty())
        .or_else(|| result.stdout.lines().rev().find(|line| !line.trim().is_empty()))
        .unwrap_or("归档操作失败。")
        .trim()
        .to_string()
}

struct EnginePaths {
    exe: PathBuf,
}

fn engine_directory() -> Result<PathBuf, String> {
    #[cfg(windows)]
    let base = std::env::var_os("LOCALAPPDATA").map(PathBuf::from);
    #[cfg(target_os = "macos")]
    let base = std::env::var_os("HOME").map(|home| PathBuf::from(home).join("Library").join("Application Support"));
    let base = base.unwrap_or_else(std::env::temp_dir);
    Ok(base.join("zip233").join("runtime").join(env!("CARGO_PKG_VERSION")))
}

fn write_if_different(path: &Path, bytes: &[u8], executable: bool) -> Result<(), String> {
    if std::fs::read(path).is_ok_and(|existing| existing == bytes) {
        return Ok(());
    }
    let parent = path.parent().ok_or_else(|| "归档引擎目录无效。".to_string())?;
    std::fs::create_dir_all(parent).map_err(|error| format!("无法准备归档引擎目录：{error}"))?;
    let temporary = parent.join(format!(".zip233-{}-{}.tmp", std::process::id(), path.file_name().unwrap_or_default().to_string_lossy()));
    std::fs::write(&temporary, bytes).map_err(|error| format!("无法修复归档引擎：{error}"))?;
    #[cfg(unix)]
    if executable {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&temporary, std::fs::Permissions::from_mode(0o755))
            .map_err(|error| format!("无法设置归档引擎权限：{error}"))?;
    }
    #[cfg(not(unix))]
    let _ = executable;
    if path.exists() {
        std::fs::remove_file(path).map_err(|error| format!("无法替换损坏的归档引擎：{error}"))?;
    }
    std::fs::rename(&temporary, path).map_err(|error| format!("无法安装归档引擎：{error}"))?;
    Ok(())
}

fn ensure_engine() -> Result<EnginePaths, String> {
    let directory = engine_directory()?;
    #[cfg(windows)]
    let exe_name = "7z.exe";
    #[cfg(target_os = "macos")]
    let exe_name = "7zz";
    let exe = directory.join(exe_name);
    write_if_different(&exe, embedded::ENGINE_EXE, true)?;
    if let Some((name, contents)) = embedded::ENGINE_SUPPORT {
        write_if_different(&directory.join(name), contents, false)?;
    }
    Ok(EnginePaths { exe })
}

fn parse_launch_request() -> Option<LaunchRequest> {
    let mut args = std::env::args_os().skip(1);
    let action = args.next()?.to_string_lossy().into_owned();
    let path = args.next().map(|value| value.to_string_lossy().into_owned());
    Some(LaunchRequest { action, path })
}

fn repair_shell_integration_on_startup() {
    #[cfg(windows)]
    if windows_shell::is_enabled() {
        if let Err(error) = windows_shell::write_entries() {
            tracing::warn!(%error, "zip233 context menu self-repair failed");
        }
    }
}

fn shell_integration_status() -> ShellIntegrationStatus {
    #[cfg(windows)]
    {
        return ShellIntegrationStatus {
            enabled: windows_shell::is_enabled(),
            healthy: windows_shell::is_registered(),
        };
    }
    #[cfg(not(windows))]
    ShellIntegrationStatus { enabled: false, healthy: false }
}

fn register_shell_integration() -> Result<(), String> {
    #[cfg(windows)]
    {
        return windows_shell::register();
    }
    #[cfg(not(windows))]
    Err("Windows 右键菜单仅适用于 Windows。".into())
}

fn unregister_shell_integration() -> Result<(), String> {
    #[cfg(windows)]
    {
        return windows_shell::unregister();
    }
    #[cfg(not(windows))]
    Err("Windows 右键菜单仅适用于 Windows。".into())
}

#[cfg(windows)]
mod windows_shell {
    use super::ARCHIVE_EXTENSIONS;
    use std::io;
    use winreg::enums::{HKEY_CURRENT_USER, KEY_READ};
    use winreg::RegKey;

    const SETTINGS_KEY: &str = "Software\\neko233\\zip233";
    const ENABLED_VALUE: &str = "ShellIntegrationEnabled";
    const ARCHIVE_VERB: &str = "SystemFileAssociations\\{}\\shell\\zip233.open";
    const DIRECTORY_VERB: &str = r"Directory\shell\zip233.compress";
    const BACKGROUND_VERB: &str = r"Directory\Background\shell\zip233.compress-here";

    fn classes_key() -> Result<RegKey, String> {
        RegKey::predef(HKEY_CURRENT_USER)
            .create_subkey("Software\\Classes")
            .map(|(key, _)| key)
            .map_err(|error| format!("无法打开当前用户文件关联：{error}"))
    }

    fn settings_key(write: bool) -> Result<RegKey, String> {
        let root = RegKey::predef(HKEY_CURRENT_USER);
        if write {
            root.create_subkey(SETTINGS_KEY)
                .map(|(key, _)| key)
                .map_err(|error| format!("无法保存 zip233 集成设置：{error}"))
        } else {
            root.open_subkey_with_flags(SETTINGS_KEY, KEY_READ)
                .map_err(|error| error.to_string())
        }
    }

    pub fn is_enabled() -> bool {
        settings_key(false)
            .and_then(|key| key.get_value::<u32, _>(ENABLED_VALUE).map_err(|error| error.to_string()))
            .is_ok_and(|value| value == 1)
    }

    fn write_enabled(enabled: bool) -> Result<(), String> {
        settings_key(true)?
            .set_value(ENABLED_VALUE, &u32::from(enabled))
            .map_err(|error| format!("无法保存 zip233 集成设置：{error}"))
    }

    fn set_verb(classes: &RegKey, relative: &str, label: &str, command_line: &str, icon: &str) -> Result<(), String> {
        let (verb, _) = classes
            .create_subkey(relative)
            .map_err(|error| format!("无法修复 Windows 右键菜单：{error}"))?;
        set_string_if_changed(&verb, "MUIVerb", label)?;
        set_string_if_changed(&verb, "Icon", icon)?;
        let (command, _) = verb
            .create_subkey("command")
            .map_err(|error| format!("无法修复 Windows 右键命令：{error}"))?;
        set_string_if_changed(&command, "", command_line)
    }

    fn set_string_if_changed(key: &RegKey, name: &str, expected: &str) -> Result<(), String> {
        if key.get_value::<String, _>(name).is_ok_and(|current| current == expected) {
            return Ok(());
        }
        key.set_value(name, &expected)
            .map_err(|error| format!("无法修复 Windows 右键注册项：{error}"))
    }

    pub fn write_entries() -> Result<(), String> {
        let exe = std::env::current_exe().map_err(|error| format!("无法确定 zip233 安装路径：{error}"))?;
        let exe = exe.to_string_lossy().into_owned();
        let quoted_exe = format!("\"{exe}\"");
        let classes = classes_key()?;
        for extension in ARCHIVE_EXTENSIONS {
            let relative = ARCHIVE_VERB.replace("{}", &format!(".{extension}"));
            let command_line = format!("{quoted_exe} open \"%1\"");
            set_verb(&classes, &relative, "使用 zip233 打开", &command_line, &exe)?;
        }
        set_verb(
            &classes,
            DIRECTORY_VERB,
            "压缩为 zip233.7z",
            &format!("{quoted_exe} compress \"%1\""),
            &exe,
        )?;
        set_verb(
            &classes,
            BACKGROUND_VERB,
            "在此处创建 zip233.7z",
            &format!("{quoted_exe} compress-here \"%V\""),
            &exe,
        )
    }

    pub fn register() -> Result<(), String> {
        write_entries()?;
        write_enabled(true)
    }

    pub fn is_registered() -> bool {
        if !is_enabled() {
            return false;
        }
        let Ok(exe) = std::env::current_exe() else { return false };
        let Ok(classes) = RegKey::predef(HKEY_CURRENT_USER).open_subkey_with_flags("Software\\Classes", KEY_READ) else { return false };
        let exe = exe.to_string_lossy().into_owned();
        let quoted_exe = format!("\"{exe}\"");
        for extension in ARCHIVE_EXTENSIONS {
            let relative = ARCHIVE_VERB.replace("{}", &format!(".{extension}"));
            if !verb_matches(&classes, &relative, &format!("{quoted_exe} open \"%1\""), &exe) {
                return false;
            }
        }
        verb_matches(&classes, DIRECTORY_VERB, &format!("{quoted_exe} compress \"%1\""), &exe)
            && verb_matches(&classes, BACKGROUND_VERB, &format!("{quoted_exe} compress-here \"%V\""), &exe)
    }

    fn verb_matches(classes: &RegKey, relative: &str, expected_command: &str, expected_icon: &str) -> bool {
        let Ok(verb) = classes.open_subkey(relative) else { return false };
        let Ok(command) = verb.open_subkey("command") else { return false };
        verb.get_value::<String, _>("MUIVerb").is_ok_and(|value| !value.is_empty())
            && verb.get_value::<String, _>("Icon").is_ok_and(|value| value == expected_icon)
            && command.get_value::<String, _>("").is_ok_and(|value| value == expected_command)
    }

    fn remove_owned_key(classes: &RegKey, relative: &str) -> Result<(), String> {
        match classes.delete_subkey_all(relative) {
            Ok(()) => Ok(()),
            Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
            Err(error) => Err(format!("无法移除 zip233 右键菜单：{error}")),
        }
    }

    pub fn unregister() -> Result<(), String> {
        let classes = classes_key()?;
        for extension in ARCHIVE_EXTENSIONS {
            let relative = ARCHIVE_VERB.replace("{}", &format!(".{extension}"));
            remove_owned_key(&classes, &relative)?;
        }
        remove_owned_key(&classes, DIRECTORY_VERB)?;
        remove_owned_key(&classes, BACKGROUND_VERB)?;
        write_enabled(false)
    }
}
