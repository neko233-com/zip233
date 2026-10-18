use anyhow::{anyhow, Context, Result};
use clap::{ArgGroup, Parser, Subcommand};
use serde::Serialize;
use std::io::{self, Read};
use std::path::{Path, PathBuf};
use zip233::archive;

#[derive(Parser)]
#[command(
    name = "zip233-cli",
    version,
    about = "zip233 内置归档工具",
    long_about = "zip233-cli 使用随程序编译的 Rust 归档库读写文件。它不需要安装 7-Zip 或其他归档命令行工具。",
    after_help = "示例:\n  zip233-cli formats\n  zip233-cli list 项目.zip\n  zip233-cli extract 项目.7z --ask-password\n  zip233-cli test 项目.rar --password-stdin\n  zip233-cli create 备份.7z ./项目 ./README.md --password-stdin\n  zip233-cli create 资料.zip ./资料 --format zip --ask-password"
)]
struct Cli {
    #[arg(short, long, global = true, help = "显示完整错误原因")]
    verbose: bool,
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    #[command(alias = "l", about = "列出归档目录内容")]
    List {
        #[arg(value_name = "ARCHIVE", help = "归档文件路径")]
        archive: PathBuf,
        #[arg(long, help = "以 JSON 输出目录内容")]
        json: bool,
        #[command(flatten)]
        password: PasswordOptions,
    },
    #[command(alias = "x", about = "安全解压归档到指定目录")]
    Extract {
        #[arg(value_name = "ARCHIVE", help = "归档文件路径")]
        archive: PathBuf,
        #[arg(
            short,
            long,
            value_name = "DIR",
            help = "输出目录；默认使用归档同名目录"
        )]
        output: Option<PathBuf>,
        #[arg(long, help = "以 JSON 输出结果")]
        json: bool,
        #[command(flatten)]
        password: PasswordOptions,
    },
    #[command(alias = "t", about = "读取归档内容并检查完整性")]
    Test {
        #[arg(value_name = "ARCHIVE", help = "归档文件路径")]
        archive: PathBuf,
        #[arg(long, help = "以 JSON 输出结果")]
        json: bool,
        #[command(flatten)]
        password: PasswordOptions,
    },
    #[command(alias = "c", about = "创建压缩包或单文件压缩流")]
    Create {
        #[arg(value_name = "OUTPUT", help = "输出归档路径")]
        output: PathBuf,
        #[arg(value_name = "INPUT", required = true, num_args = 1.., help = "一个或多个输入文件或目录")]
        inputs: Vec<PathBuf>,
        #[arg(long, value_name = "FORMAT", help = "格式；默认根据输出扩展名判断")]
        format: Option<String>,
        #[arg(long, default_value = "5", value_parser = ["1", "5", "7", "9"], help = "压缩等级：1 快、5 标准、7 较高、9 最高")]
        level: String,
        #[arg(long, help = "以 JSON 输出结果")]
        json: bool,
        #[command(flatten)]
        password: PasswordOptions,
    },
    #[command(about = "显示内置格式、创建能力和密码保护能力")]
    Formats {
        #[arg(long, help = "以 JSON 输出格式信息")]
        json: bool,
    },
}

#[derive(clap::Args, Default)]
#[command(group(ArgGroup::new("password-source").multiple(false)))]
struct PasswordOptions {
    #[arg(
        long,
        group = "password-source",
        help = "从标准输入读取密码的一行；适合脚本和管道"
    )]
    password_stdin: bool,
    #[arg(long, group = "password-source", help = "交互式隐藏输入密码")]
    ask_password: bool,
}

#[derive(Serialize)]
struct FormatReport<'a> {
    readable_extensions: &'a [&'a str],
    creatable_formats: &'a [&'a str],
    encrypted_read: &'static str,
    encrypted_create: &'static str,
    notes: &'static [&'static str],
}

fn main() {
    let cli = Cli::parse();
    if let Err(error) = dispatch(cli.command) {
        if cli.verbose {
            eprintln!("错误：{error:#}");
        } else {
            eprintln!("错误：{error}");
        }
        std::process::exit(1);
    }
}

fn dispatch(command: Command) -> Result<()> {
    match command {
        Command::List {
            archive: path,
            json,
            password,
        } => {
            let password = resolve_password(password)?;
            let path_text = path.to_string_lossy();
            let entries = archive::list_archive(&path_text, password.as_deref())
                .map_err(|error| anyhow!(error))
                .with_context(|| format!("读取归档失败：{}", path.display()))?;
            if json {
                print_json(&entries)?;
            } else {
                print_entries(&entries);
            }
            Ok(())
        }
        Command::Extract {
            archive: path,
            output,
            json,
            password,
        } => {
            let password = resolve_password(password)?;
            let output = output.unwrap_or_else(|| default_output_dir(&path));
            let path_text = path.to_string_lossy();
            let output_text = output.to_string_lossy();
            let result = archive::extract_archive(&path_text, &output_text, password.as_deref())
                .map_err(|error| anyhow!(error))
                .with_context(|| format!("解压失败：{}", path.display()))?;
            print_result(result, json)?;
            if !json {
                println!("输出目录：{}", output.display());
            }
            Ok(())
        }
        Command::Test {
            archive: path,
            json,
            password,
        } => {
            let password = resolve_password(password)?;
            let path_text = path.to_string_lossy();
            let result = archive::test_archive(&path_text, password.as_deref())
                .map_err(|error| anyhow!(error))
                .with_context(|| format!("检查失败：{}", path.display()))?;
            let ok = result.ok;
            print_result(result, json)?;
            if !ok {
                return Err(anyhow!("完整性检查未通过"));
            }
            Ok(())
        }
        Command::Create {
            output,
            inputs,
            format,
            level,
            json,
            password,
        } => {
            let format = format
                .or_else(|| infer_format(&output))
                .ok_or_else(|| anyhow!("无法从输出扩展名判断格式，请使用 --format 指定。"))?;
            let password = resolve_password(password)?;
            let input_text = inputs
                .iter()
                .map(|path| path.to_string_lossy().into_owned())
                .collect::<Vec<_>>();
            let output_text = output.to_string_lossy();
            let result = archive::create_archive(
                &input_text,
                &output_text,
                &format,
                &level,
                password.as_deref(),
            )
            .map_err(|error| anyhow!(error))
            .with_context(|| format!("创建归档失败：{}", output.display()))?;
            print_result(result, json)?;
            if !json {
                println!("输出文件：{}", output.display());
            }
            Ok(())
        }
        Command::Formats { json } => {
            let report = FormatReport {
                readable_extensions: archive::ARCHIVE_EXTENSIONS,
                creatable_formats: archive::CREATE_FORMATS,
                encrypted_read: "7z、ZIP、RAR，以及 unarc-rs 所支持的加密经典格式（按各格式能力）",
                encrypted_create: "7z AES-256、ZIP AES-256（加密文件内容；ZIP 文件名仍可见）",
                notes: &[
                    "读取覆盖所列扩展名；扩展名匹配不保证每个变体或损坏文件都可解码。",
                    "TAR、GZIP、BZIP2、XZ 本身不提供密码加密。",
                    "RAR 和经典格式可读取/解压；创建能力见 creatable_formats。",
                    "格式清单不包含专有或未公开格式，也不等于磁盘镜像挂载能力。",
                ],
            };
            if json {
                print_json(&report)?;
            } else {
                println!(
                    "内置读取格式：{}",
                    report
                        .readable_extensions
                        .iter()
                        .map(|ext| format!(".{ext}"))
                        .collect::<Vec<_>>()
                        .join("  ")
                );
                println!("可创建格式：{}", report.creatable_formats.join("  "));
                println!("加密读取：{}", report.encrypted_read);
                println!("加密创建：{}", report.encrypted_create);
                for note in report.notes {
                    println!("说明：{note}");
                }
            }
            Ok(())
        }
    }
}

fn resolve_password(options: PasswordOptions) -> Result<Option<String>> {
    if options.password_stdin {
        let mut password = String::new();
        io::stdin()
            .read_to_string(&mut password)
            .context("无法从标准输入读取密码")?;
        while password.ends_with(['\r', '\n']) {
            password.pop();
        }
        if password.is_empty() {
            return Err(anyhow!("标准输入没有提供密码。"));
        }
        return Ok(Some(password));
    }
    if options.ask_password {
        let password =
            rpassword::prompt_password("归档密码（输入不显示）：").context("无法读取交互式密码")?;
        if password.is_empty() {
            return Err(anyhow!("密码不能为空。"));
        }
        return Ok(Some(password));
    }
    Ok(None)
}

fn print_entries(entries: &[zip233::ArchiveItem]) {
    println!("{:<10} {:>12} {:>12}  名称", "类型", "原始大小", "压缩大小");
    println!("{}", "─".repeat(78));
    for entry in entries {
        let kind = if entry.is_directory {
            "目录"
        } else {
            "文件"
        };
        let size = entry.size.map(format_size).unwrap_or_else(|| "—".into());
        let packed = entry
            .packed_size
            .map(format_size)
            .unwrap_or_else(|| "—".into());
        println!("{kind:<10} {size:>12} {packed:>12}  {}", entry.name);
    }
    let file_count = entries.iter().filter(|entry| !entry.is_directory).count();
    let total_size = entries
        .iter()
        .filter(|entry| !entry.is_directory)
        .filter_map(|entry| entry.size)
        .sum::<u64>();
    println!(
        "\n共 {} 项文件，原始大小 {}",
        file_count,
        format_size(total_size)
    );
}

fn print_result<T: Serialize>(result: T, json: bool) -> Result<()> {
    if json {
        print_json(&result)
    } else {
        let value = serde_json::to_value(result)?;
        if let Some(message) = value.get("stdout").and_then(serde_json::Value::as_str) {
            println!("{message}");
        }
        if let Some(message) = value
            .get("stderr")
            .and_then(serde_json::Value::as_str)
            .filter(|message| !message.is_empty())
        {
            eprintln!("错误：{message}");
        }
        Ok(())
    }
}

fn print_json(value: &impl Serialize) -> Result<()> {
    println!("{}", serde_json::to_string_pretty(value)?);
    Ok(())
}

fn format_size(bytes: u64) -> String {
    const UNITS: [&str; 5] = ["B", "KiB", "MiB", "GiB", "TiB"];
    let mut value = bytes as f64;
    let mut unit = 0;
    while value >= 1024.0 && unit < UNITS.len() - 1 {
        value /= 1024.0;
        unit += 1;
    }
    if unit == 0 {
        format!("{bytes} B")
    } else {
        format!("{value:.1} {}", UNITS[unit])
    }
}

fn default_output_dir(archive: &Path) -> PathBuf {
    let file_name = archive
        .file_name()
        .map(|value| value.to_string_lossy().into_owned())
        .unwrap_or_else(|| "解压内容".into());
    let lower = file_name.to_ascii_lowercase();
    let suffixes = [
        ".tar.bz2", ".tar.gz", ".tar.xz", ".tar.zst", ".tbz2", ".tbz", ".tgz", ".txz", ".7z",
        ".zip", ".rar", ".tar", ".gz", ".bz2", ".xz", ".zst", ".jar", ".lha", ".lzh", ".ace",
        ".arj", ".arc", ".pak", ".zoo", ".ha", ".uc2", ".sqz", ".sq2", ".sq", ".ice",
    ];
    let base = suffixes
        .iter()
        .find_map(|suffix| {
            lower
                .strip_suffix(suffix)
                .map(|_| file_name[..file_name.len() - suffix.len()].to_string())
        })
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| format!("{file_name}-解压"));
    archive
        .parent()
        .unwrap_or_else(|| Path::new("."))
        .join(base)
}

fn infer_format(output: &Path) -> Option<String> {
    let lower = output.file_name()?.to_string_lossy().to_ascii_lowercase();
    for (suffix, format) in [
        (".tar.gz", "tar.gz"),
        (".tgz", "tar.gz"),
        (".tar.bz2", "tar.bz2"),
        (".tbz2", "tar.bz2"),
        (".tbz", "tar.bz2"),
        (".tar.xz", "tar.xz"),
        (".txz", "tar.xz"),
        (".7z", "7z"),
        (".zip", "zip"),
        (".tar", "tar"),
        (".gz", "gzip"),
        (".bz2", "bzip2"),
        (".xz", "xz"),
    ] {
        if lower.ends_with(suffix) {
            return Some(format.into());
        }
    }
    None
}
