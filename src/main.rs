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
use std::sync::OnceLock;
use zip233::{archive, update};

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
        Ok(data) => IpcResponse {
            id,
            success: true,
            data,
        },
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
                .add_filter("归档文件", ARCHIVE_EXTENSIONS)
                .pick_file();
            Ok(path
                .map(|value| value.to_string_lossy().into_owned())
                .into())
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
            Ok(path
                .map(|value| value.to_string_lossy().into_owned())
                .into())
        }
        "pick_output_folder" => {
            let path = rfd::FileDialog::new()
                .set_title("选择解压目标文件夹")
                .pick_folder();
            Ok(path
                .map(|value| value.to_string_lossy().into_owned())
                .into())
        }
        "save_archive" => {
            let extension = payload
                .get("extension")
                .and_then(Value::as_str)
                .filter(|value| archive::supported_create_format(value))
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
            Ok(path
                .map(|value| value.to_string_lossy().into_owned())
                .into())
        }
        "list_archive" => {
            let path = required_string(payload, "path")?;
            let items = archive::list_archive(path, optional_string(payload, "password"))?;
            Ok(serde_json::to_value(items).map_err(|e| e.to_string())?)
        }
        "test_archive" => {
            let path = required_string(payload, "path")?;
            let result = archive::test_archive(path, optional_string(payload, "password"))?;
            serde_json::to_value(result).map_err(|e| e.to_string())
        }
        "extract_archive" => {
            let path = required_string(payload, "path")?;
            let output_dir = required_string(payload, "outputDir")?;
            let result =
                archive::extract_archive(path, output_dir, optional_string(payload, "password"))?;
            serde_json::to_value(result).map_err(|e| e.to_string())
        }
        "create_archive" => {
            let paths = payload
                .get("paths")
                .and_then(Value::as_array)
                .ok_or_else(|| "请选择要压缩的文件或文件夹。".to_string())?
                .iter()
                .map(|value| {
                    value
                        .as_str()
                        .map(str::to_string)
                        .ok_or_else(|| "文件路径无效。".to_string())
                })
                .collect::<Result<Vec<_>, _>>()?;
            let target = required_string(payload, "target")?;
            let format = required_string(payload, "format")?;
            let level = payload.get("level").and_then(Value::as_str).unwrap_or("5");
            let password = optional_string(payload, "password");
            let result = archive::create_archive(&paths, target, format, level, password)?;
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
        "check_updates" => {
            serde_json::to_value(update::check_for_updates()?).map_err(|error| error.to_string())
        }
        "download_update" => {
            serde_json::to_value(update::start_download()?).map_err(|error| error.to_string())
        }
        "update_download_status" => {
            serde_json::to_value(update::current_download()).map_err(|error| error.to_string())
        }
        "install_update" => {
            update::open_downloaded_update()?;
            Ok(json!({ "ok": true }))
        }
        _ => Err(format!("未知的 zip233 操作：{command}")),
    }
}

const ARCHIVE_EXTENSIONS: &[&str] = archive::ARCHIVE_EXTENSIONS;
const LEGACY_ARCHIVE_EXTENSIONS: &[&str] = &[
    "zst", "lz", "lzma", "cab", "wim", "iso", "dmg", "rpm", "deb", "cpio", "chm", "vhd", "vhdx",
    "vmdk", "vdi", "qcow2", "udf", "xar", "apfs", "squashfs", "cramfs", "fat", "ntfs", "gpt",
    "mbr", "uefi", "ihex", "nsis", "msi", "hfs",
];

fn required_string<'a>(payload: &'a Value, name: &str) -> Result<&'a str, String> {
    payload
        .get(name)
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| format!("缺少有效参数：{name}"))
}

fn optional_string<'a>(payload: &'a Value, name: &str) -> Option<&'a str> {
    payload
        .get(name)
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty())
}

fn load_window_icon() -> Option<rdesktop_core::WindowIcon> {
    let image = image::load_from_memory(include_bytes!("../resources/icons/zip233-256.png"))
        .ok()?
        .into_rgba8();
    let (width, height) = image.dimensions();
    Some(rdesktop_core::WindowIcon {
        rgba: image.into_raw(),
        width,
        height,
    })
}

fn parse_launch_request() -> Option<LaunchRequest> {
    let mut args = std::env::args_os().skip(1);
    let action = args.next()?.to_string_lossy().into_owned();
    let path = args
        .next()
        .map(|value| value.to_string_lossy().into_owned());
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
    ShellIntegrationStatus {
        enabled: false,
        healthy: false,
    }
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
    use super::{ARCHIVE_EXTENSIONS, LEGACY_ARCHIVE_EXTENSIONS};
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
            .and_then(|key| {
                key.get_value::<u32, _>(ENABLED_VALUE)
                    .map_err(|error| error.to_string())
            })
            .is_ok_and(|value| value == 1)
    }

    fn write_enabled(enabled: bool) -> Result<(), String> {
        settings_key(true)?
            .set_value(ENABLED_VALUE, &u32::from(enabled))
            .map_err(|error| format!("无法保存 zip233 集成设置：{error}"))
    }

    fn set_verb(
        classes: &RegKey,
        relative: &str,
        label: &str,
        command_line: &str,
        icon: &str,
    ) -> Result<(), String> {
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
        if key
            .get_value::<String, _>(name)
            .is_ok_and(|current| current == expected)
        {
            return Ok(());
        }
        key.set_value(name, &expected)
            .map_err(|error| format!("无法修复 Windows 右键注册项：{error}"))
    }

    pub fn write_entries() -> Result<(), String> {
        let exe = std::env::current_exe()
            .map_err(|error| format!("无法确定 zip233 安装路径：{error}"))?;
        let exe = exe.to_string_lossy().into_owned();
        let quoted_exe = format!("\"{exe}\"");
        let classes = classes_key()?;
        for extension in LEGACY_ARCHIVE_EXTENSIONS {
            let relative = ARCHIVE_VERB.replace("{}", &format!(".{extension}"));
            remove_owned_key(&classes, &relative)?;
        }
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
        let Ok(exe) = std::env::current_exe() else {
            return false;
        };
        let Ok(classes) =
            RegKey::predef(HKEY_CURRENT_USER).open_subkey_with_flags("Software\\Classes", KEY_READ)
        else {
            return false;
        };
        let exe = exe.to_string_lossy().into_owned();
        let quoted_exe = format!("\"{exe}\"");
        for extension in ARCHIVE_EXTENSIONS {
            let relative = ARCHIVE_VERB.replace("{}", &format!(".{extension}"));
            if !verb_matches(
                &classes,
                &relative,
                &format!("{quoted_exe} open \"%1\""),
                &exe,
            ) {
                return false;
            }
        }
        verb_matches(
            &classes,
            DIRECTORY_VERB,
            &format!("{quoted_exe} compress \"%1\""),
            &exe,
        ) && verb_matches(
            &classes,
            BACKGROUND_VERB,
            &format!("{quoted_exe} compress-here \"%V\""),
            &exe,
        )
    }

    fn verb_matches(
        classes: &RegKey,
        relative: &str,
        expected_command: &str,
        expected_icon: &str,
    ) -> bool {
        let Ok(verb) = classes.open_subkey(relative) else {
            return false;
        };
        let Ok(command) = verb.open_subkey("command") else {
            return false;
        };
        verb.get_value::<String, _>("MUIVerb")
            .is_ok_and(|value| !value.is_empty())
            && verb
                .get_value::<String, _>("Icon")
                .is_ok_and(|value| value == expected_icon)
            && command
                .get_value::<String, _>("")
                .is_ok_and(|value| value == expected_command)
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
        for extension in LEGACY_ARCHIVE_EXTENSIONS {
            let relative = ARCHIVE_VERB.replace("{}", &format!(".{extension}"));
            remove_owned_key(&classes, &relative)?;
        }
        remove_owned_key(&classes, DIRECTORY_VERB)?;
        remove_owned_key(&classes, BACKGROUND_VERB)?;
        write_enabled(false)
    }
}
