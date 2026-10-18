use crate::{ArchiveItem, ToolResult};
use bzip2::{write::BzEncoder, Compression as BzipCompression};
use flate2::{write::GzEncoder, Compression as GzipCompression};
use lzma_rust2::{XzOptions, XzReader, XzWriter};
use sevenz_rust2::{encoder_options::Lzma2Options, ArchiveWriter};
use std::collections::HashSet;
use std::fs::{self, File};
use std::io::{self, BufReader, Write};
use std::path::{Component, Path, PathBuf};
use tar::Builder as TarBuilder;
use tempfile::TempDir;
use unarc_rs::unified::{ArchiveFormat, ArchiveOptions, UnifiedArchive};
use zip::write::{SimpleFileOptions, ZipWriter};
use zip::CompressionMethod;

pub const ARCHIVE_EXTENSIONS: &[&str] = &[
    "7z", "zip", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz", "Z", "taz", "ace",
    "arj", "arc", "pak", "zoo", "ha", "jar", "j", "uc2", "sq", "sq2", "qqq", "sqz", "hyp", "lzh",
    "lha", "ice", "pi9",
];

pub const CREATE_FORMATS: &[&str] = &[
    "7z", "zip", "tar", "tar.gz", "tar.xz", "tar.bz2", "gzip", "bzip2", "xz",
];

pub fn supported_create_format(format: &str) -> bool {
    matches!(
        format,
        "7z" | "zip" | "tar" | "tar.gz" | "tar.xz" | "tar.bz2" | "gzip" | "bzip2" | "xz"
    )
}

pub fn list_archive(path: &str, password: Option<&str>) -> Result<Vec<ArchiveItem>, String> {
    if is_raw_xz(path) {
        let (size, packed_size) = measure_xz(path)?;
        let name = Path::new(path)
            .file_stem()
            .map(|value| value.to_string_lossy().into_owned())
            .unwrap_or_else(|| "内容".into());
        return Ok(vec![ArchiveItem {
            name,
            size: Some(size),
            packed_size: Some(packed_size),
            is_directory: false,
            modified: None,
        }]);
    }

    let mut opened = open_archive(path, password)?;
    let mut items = Vec::new();
    while let Some(entry) = opened.archive.next_entry().map_err(format_error)? {
        let name = entry.name().to_string();
        items.push(ArchiveItem {
            is_directory: name.ends_with('/') || name.ends_with('\\'),
            name,
            size: Some(entry.original_size()),
            packed_size: Some(entry.compressed_size()),
            modified: None,
        });
    }
    Ok(items)
}

pub fn test_archive(path: &str, password: Option<&str>) -> Result<ToolResult, String> {
    let result = if is_raw_xz(path) {
        let mut decoder = XzReader::new(File::open(path).map_err(format_error)?, true);
        io::copy(&mut decoder, &mut io::sink()).map(|_| ())
    } else {
        let mut opened = open_archive(path, password)?;
        let mut sink = io::sink();
        let mut operation = Ok(());
        while let Some(entry) = opened.archive.next_entry().map_err(format_error)? {
            if let Err(error) = opened.archive.read_to(&entry, &mut sink) {
                operation = Err(io::Error::other(error.to_string()));
                break;
            }
        }
        operation
    };
    Ok(match result {
        Ok(()) => success("归档完整性检查通过"),
        Err(error) => failure(error.to_string()),
    })
}

pub fn extract_archive(
    path: &str,
    output_dir: &str,
    password: Option<&str>,
) -> Result<ToolResult, String> {
    let output_root = PathBuf::from(output_dir);
    fs::create_dir_all(&output_root).map_err(|error| format!("无法创建解压目录：{error}"))?;
    let canonical_root =
        fs::canonicalize(&output_root).map_err(|error| format!("无法访问解压目录：{error}"))?;

    if is_raw_xz(path) {
        let output_name = Path::new(path)
            .file_stem()
            .ok_or_else(|| "无法确定 XZ 文件解压后的名称。".to_string())?
            .to_string_lossy();
        let relative = safe_relative_path(&output_name)?;
        let target = prepare_output_path(&canonical_root, &relative, false)?;
        let mut decoder = XzReader::new(File::open(path).map_err(format_error)?, true);
        let mut output = File::create(target).map_err(format_error)?;
        io::copy(&mut decoder, &mut output).map_err(format_error)?;
        return Ok(success("已解压 XZ 文件"));
    }

    let mut opened = open_archive(path, password)?;
    let mut extracted = 0_u64;
    while let Some(entry) = opened.archive.next_entry().map_err(format_error)? {
        let name = entry.name().to_string();
        let is_directory = name.ends_with('/') || name.ends_with('\\');
        let relative = safe_relative_path(&name)?;
        let target = prepare_output_path(&canonical_root, &relative, is_directory)?;
        if is_directory {
            continue;
        }
        let mut output = File::create(target).map_err(format_error)?;
        opened
            .archive
            .read_to(&entry, &mut output)
            .map_err(format_error)?;
        extracted += 1;
    }
    Ok(success(&format!("已解压 {extracted} 个文件")))
}

pub fn create_archive(
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
    let level = level
        .parse::<u32>()
        .ok()
        .filter(|value| matches!(value, 1 | 5 | 7 | 9))
        .ok_or_else(|| "压缩等级无效。".to_string())?;
    if matches!(format, "gzip" | "bzip2" | "xz") && paths.len() != 1 {
        return Err("GZIP、BZIP2 和 XZ 是单文件压缩格式，请只选择一个文件；压缩多个文件请选 TAR.GZ、TAR.BZ2 或 TAR.XZ。".into());
    }
    if password.is_some_and(|value| !value.is_empty()) && !matches!(format, "7z" | "zip") {
        return Err(
            "当前只有 7z 和 ZIP 支持创建加密归档；TAR、GZIP、BZIP2 和 XZ 本身不提供加密。".into(),
        );
    }

    let target_path = Path::new(target);
    match format {
        "7z" => create_7z(paths, target_path, level, password)?,
        "zip" => create_zip(paths, target_path, level, password)?,
        "tar" => create_tar(paths, target_path)?,
        "tar.gz" => create_tar_gzip(paths, target_path, level)?,
        "tar.bz2" => create_tar_bzip2(paths, target_path, level)?,
        "tar.xz" => create_tar_xz(paths, target_path, level)?,
        "gzip" => create_gzip(&paths[0], target_path, level)?,
        "bzip2" => create_bzip2(&paths[0], target_path, level)?,
        "xz" => create_xz(&paths[0], target_path, level)?,
        _ => unreachable!("format checked above"),
    }
    Ok(success("归档已创建"))
}

struct OpenedArchive {
    archive: UnifiedArchive<BufReader<File>>,
    _temporary: Option<TempDir>,
}

fn open_archive(path: &str, password: Option<&str>) -> Result<OpenedArchive, String> {
    let input_path = Path::new(path);
    let options = archive_options(password);
    if is_tar_xz(path) {
        let temporary = tempfile::Builder::new()
            .prefix("zip233-xz-")
            .tempdir()
            .map_err(format_error)?;
        let tar_path = temporary.path().join("contents.tar");
        let mut decoder = XzReader::new(File::open(input_path).map_err(format_error)?, true);
        let mut output = File::create(&tar_path).map_err(format_error)?;
        io::copy(&mut decoder, &mut output).map_err(format_error)?;
        drop(output);
        let archive = ArchiveFormat::Tar
            .open_with_options(
                BufReader::new(File::open(tar_path).map_err(format_error)?),
                options,
            )
            .map_err(format_error)?;
        return Ok(OpenedArchive {
            archive,
            _temporary: Some(temporary),
        });
    }

    let mut input = BufReader::new(File::open(input_path).map_err(format_error)?);
    let format = if is_tar_gzip(path) {
        ArchiveFormat::Tgz
    } else if is_tar_bzip2(path) {
        ArchiveFormat::Tbz
    } else {
        ArchiveFormat::detect(&mut input, Some(input_path))
            .map_err(format_error)?
            .ok_or_else(|| "无法识别该归档格式。".to_string())?
    };
    let archive = format
        .open_with_options(input, options)
        .map_err(format_error)?;
    Ok(OpenedArchive {
        archive,
        _temporary: None,
    })
}

fn archive_options(password: Option<&str>) -> ArchiveOptions {
    let options = ArchiveOptions::new().with_verify_crc(true);
    match password {
        Some(password) => options.with_password(password),
        None => options,
    }
}

fn is_tar_xz(path: &str) -> bool {
    let path = path.to_ascii_lowercase();
    path.ends_with(".tar.xz") || path.ends_with(".txz")
}

fn is_tar_gzip(path: &str) -> bool {
    let path = path.to_ascii_lowercase();
    path.ends_with(".tar.gz") || path.ends_with(".tgz")
}

fn is_tar_bzip2(path: &str) -> bool {
    let path = path.to_ascii_lowercase();
    path.ends_with(".tar.bz2") || path.ends_with(".tbz") || path.ends_with(".tbz2")
}

fn is_raw_xz(path: &str) -> bool {
    path.to_ascii_lowercase().ends_with(".xz") && !is_tar_xz(path)
}

fn measure_xz(path: &str) -> Result<(u64, u64), String> {
    let mut decoder = XzReader::new(File::open(path).map_err(format_error)?, true);
    let mut counter = CountingWriter::default();
    io::copy(&mut decoder, &mut counter).map_err(format_error)?;
    let packed = fs::metadata(path).map_err(format_error)?.len();
    Ok((counter.0, packed))
}

#[derive(Default)]
struct CountingWriter(u64);

impl Write for CountingWriter {
    fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
        self.0 = self.0.saturating_add(buffer.len() as u64);
        Ok(buffer.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

fn safe_relative_path(name: &str) -> Result<PathBuf, String> {
    let normalized = name.replace('\\', "/");
    if normalized.is_empty() || normalized.starts_with('/') || normalized.contains(':') {
        return Err(format!("归档包含不安全的路径：{name}"));
    }
    let relative = Path::new(&normalized);
    let mut clean = PathBuf::new();
    for component in relative.components() {
        match component {
            Component::Normal(part) => clean.push(part),
            Component::CurDir => {}
            Component::ParentDir | Component::RootDir | Component::Prefix(_) => {
                return Err(format!("归档包含不安全的路径：{name}"));
            }
        }
    }
    if clean.as_os_str().is_empty() {
        return Err(format!("归档包含无效的路径：{name}"));
    }
    Ok(clean)
}

fn prepare_output_path(root: &Path, relative: &Path, directory: bool) -> Result<PathBuf, String> {
    let target = root.join(relative);
    let parent = if directory {
        target.as_path()
    } else {
        target
            .parent()
            .ok_or_else(|| "解压目标路径无效。".to_string())?
    };
    fs::create_dir_all(parent).map_err(format_error)?;
    let canonical_parent = fs::canonicalize(parent).map_err(format_error)?;
    if !canonical_parent.starts_with(root) {
        return Err("解压路径越出了目标文件夹。".into());
    }
    if !directory
        && fs::symlink_metadata(&target).is_ok_and(|metadata| metadata.file_type().is_symlink())
    {
        return Err("解压目标已是符号链接，已拒绝覆盖。".into());
    }
    Ok(target)
}

#[derive(Clone)]
struct SourceEntry {
    path: PathBuf,
    name: String,
    directory: bool,
}

fn collect_sources(paths: &[String]) -> Result<Vec<SourceEntry>, String> {
    let mut entries = Vec::new();
    let mut names = HashSet::new();
    for source in paths {
        let path = PathBuf::from(source);
        collect_source(&path, None, &mut names, &mut entries)?;
    }
    Ok(entries)
}

fn collect_source(
    path: &Path,
    archive_name: Option<String>,
    names: &mut HashSet<String>,
    entries: &mut Vec<SourceEntry>,
) -> Result<(), String> {
    let metadata = fs::symlink_metadata(path)
        .map_err(|error| format!("无法读取源文件 {}：{error}", path.display()))?;
    if metadata.file_type().is_symlink() {
        return Err(format!("当前版本不打包符号链接：{}", path.display()));
    }
    let name = archive_name.unwrap_or_else(|| {
        path.file_name()
            .map(|value| value.to_string_lossy().into_owned())
            .unwrap_or_else(|| "archive".into())
    });
    let name = name.replace('\\', "/");
    if !names.insert(name.clone()) {
        return Err(format!("所选文件包含重复归档路径：{name}"));
    }
    entries.push(SourceEntry {
        path: path.to_path_buf(),
        name: name.clone(),
        directory: metadata.is_dir(),
    });
    if metadata.is_dir() {
        let mut children = fs::read_dir(path)
            .map_err(format_error)?
            .map(|entry| entry.map(|entry| entry.path()).map_err(format_error))
            .collect::<Result<Vec<_>, _>>()?;
        children.sort();
        for child in children {
            let child_name = child
                .file_name()
                .map(|value| format!("{name}/{}", value.to_string_lossy()))
                .ok_or_else(|| "源路径名称无效。".to_string())?;
            collect_source(&child, Some(child_name), names, entries)?;
        }
    }
    Ok(())
}

fn create_7z(
    paths: &[String],
    target: &Path,
    level: u32,
    password: Option<&str>,
) -> Result<(), String> {
    let mut writer = ArchiveWriter::create(target).map_err(format_error)?;
    let mut methods = Vec::new();
    if let Some(password) = password.filter(|value| !value.is_empty()) {
        methods.push(sevenz_rust2::encoder_options::AesEncoderOptions::new(password.into()).into());
    }
    methods.push(Lzma2Options::from_level(level).into());
    writer.set_content_methods(methods);
    for path in paths {
        writer
            .push_source_path(path, |_| true)
            .map_err(format_error)?;
    }
    writer.finish().map(|_| ()).map_err(format_error)
}

fn create_zip(
    paths: &[String],
    target: &Path,
    level: u32,
    password: Option<&str>,
) -> Result<(), String> {
    let entries = collect_sources(paths)?;
    let file = File::create(target).map_err(format_error)?;
    let mut writer = ZipWriter::new(file);
    let options = SimpleFileOptions::default()
        .compression_method(CompressionMethod::Deflated)
        .compression_level(Some(i64::from(level)));
    for entry in entries {
        if entry.directory {
            writer
                .add_directory(format!("{}/", entry.name.trim_end_matches('/')), options)
                .map_err(format_error)?;
        } else {
            let file_options = match password.filter(|value| !value.is_empty()) {
                Some(password) => options.with_aes_encryption(zip::AesMode::Aes256, password),
                None => options,
            };
            writer
                .start_file(&entry.name, file_options)
                .map_err(format_error)?;
            io::copy(
                &mut File::open(entry.path).map_err(format_error)?,
                &mut writer,
            )
            .map_err(format_error)?;
        }
    }
    writer.finish().map_err(format_error)?;
    Ok(())
}

fn create_tar(paths: &[String], target: &Path) -> Result<(), String> {
    let file = File::create(target).map_err(format_error)?;
    let mut builder = TarBuilder::new(file);
    append_tar_sources(&mut builder, paths)?;
    builder.finish().map_err(format_error)
}

fn append_tar_sources<W: Write>(
    builder: &mut TarBuilder<W>,
    paths: &[String],
) -> Result<(), String> {
    let entries = collect_sources(paths)?;
    for entry in entries {
        if entry.directory {
            builder
                .append_dir(&entry.name, &entry.path)
                .map_err(format_error)?;
        } else {
            builder
                .append_path_with_name(&entry.path, &entry.name)
                .map_err(format_error)?;
        }
    }
    Ok(())
}

fn create_tar_gzip(paths: &[String], target: &Path, level: u32) -> Result<(), String> {
    let encoder = GzEncoder::new(
        File::create(target).map_err(format_error)?,
        GzipCompression::new(level),
    );
    let mut builder = TarBuilder::new(encoder);
    append_tar_sources(&mut builder, paths)?;
    builder.finish().map_err(format_error)?;
    builder
        .into_inner()
        .map_err(format_error)?
        .finish()
        .map_err(format_error)?;
    Ok(())
}

fn create_tar_bzip2(paths: &[String], target: &Path, level: u32) -> Result<(), String> {
    let encoder = BzEncoder::new(
        File::create(target).map_err(format_error)?,
        BzipCompression::new(level),
    );
    let mut builder = TarBuilder::new(encoder);
    append_tar_sources(&mut builder, paths)?;
    builder.finish().map_err(format_error)?;
    builder
        .into_inner()
        .map_err(format_error)?
        .finish()
        .map_err(format_error)?;
    Ok(())
}

fn create_tar_xz(paths: &[String], target: &Path, level: u32) -> Result<(), String> {
    let encoder = XzWriter::new(
        File::create(target).map_err(format_error)?,
        XzOptions::with_preset(level),
    )
    .map_err(format_error)?;
    let mut builder = TarBuilder::new(encoder);
    append_tar_sources(&mut builder, paths)?;
    builder.finish().map_err(format_error)?;
    builder
        .into_inner()
        .map_err(format_error)?
        .finish()
        .map_err(format_error)?;
    Ok(())
}

fn create_gzip(source: &str, target: &Path, level: u32) -> Result<(), String> {
    let mut encoder = GzEncoder::new(
        File::create(target).map_err(format_error)?,
        GzipCompression::new(level),
    );
    io::copy(&mut File::open(source).map_err(format_error)?, &mut encoder).map_err(format_error)?;
    encoder.finish().map_err(format_error)?;
    Ok(())
}

fn create_bzip2(source: &str, target: &Path, level: u32) -> Result<(), String> {
    let mut encoder = BzEncoder::new(
        File::create(target).map_err(format_error)?,
        BzipCompression::new(level),
    );
    io::copy(&mut File::open(source).map_err(format_error)?, &mut encoder).map_err(format_error)?;
    encoder.finish().map_err(format_error)?;
    Ok(())
}

fn create_xz(source: &str, target: &Path, level: u32) -> Result<(), String> {
    let mut encoder = XzWriter::new(
        File::create(target).map_err(format_error)?,
        XzOptions::with_preset(level),
    )
    .map_err(format_error)?;
    io::copy(&mut File::open(source).map_err(format_error)?, &mut encoder).map_err(format_error)?;
    encoder.finish().map_err(format_error)?;
    Ok(())
}

fn success(message: &str) -> ToolResult {
    ToolResult {
        ok: true,
        code: Some(0),
        stdout: message.into(),
        stderr: String::new(),
    }
}

fn failure(message: String) -> ToolResult {
    ToolResult {
        ok: false,
        code: Some(1),
        stdout: String::new(),
        stderr: message,
    }
}

fn format_error(error: impl std::fmt::Display) -> String {
    error.to_string()
}
