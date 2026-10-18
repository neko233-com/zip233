use std::env;
use std::fs;
use std::path::{Path, PathBuf};

fn collect_files(root: &Path, current: &Path, output: &mut Vec<PathBuf>) -> std::io::Result<()> {
    for entry in fs::read_dir(current)? {
        let entry = entry?;
        let path = entry.path();
        if path.is_dir() {
            collect_files(root, &path, output)?;
        } else if path.is_file() {
            output.push(path.strip_prefix(root).expect("file is under dist").to_path_buf());
        }
    }
    Ok(())
}

fn main() {
    let manifest_dir = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").expect("manifest dir"));
    let dist = manifest_dir.join("dist");
    let index = dist.join("index.html");
    if !index.is_file() {
        panic!("dist/index.html is missing; run `npm run build:frontend` before building zip233");
    }

    println!("cargo:rerun-if-changed={}", dist.display());
    let mut assets = Vec::new();
    collect_files(&dist, &dist, &mut assets).expect("enumerate frontend assets");
    assets.sort();

    let target_os = env::var("CARGO_CFG_TARGET_OS").expect("target operating system");
    let mut generated = String::from(
        "pub fn embedded_assets() -> Vec<(String, &'static [u8])> { vec![\n",
    );
    for relative in assets {
        let web_path = relative.to_string_lossy().replace('\\', "/");
        let source_path = dist.join(&relative);
        println!("cargo:rerun-if-changed={}", source_path.display());
        generated.push_str(&format!(
            "({:?}.to_string(), include_bytes!({:?}) as &'static [u8]),\n",
            web_path,
            source_path
        ));
    }
    generated.push_str("] }\n");

    let engines = manifest_dir.join("resources").join("engines");
    match target_os.as_str() {
        "windows" => {
            let exe = engines.join("windows").join("7z.exe");
            let dll = engines.join("windows").join("7z.dll");
            for path in [&exe, &dll] {
                if !path.is_file() {
                    panic!("{} is missing; run `npm run prepare:engine`", path.display());
                }
                println!("cargo:rerun-if-changed={}", path.display());
            }
            generated.push_str(&format!(
                "pub const ENGINE_EXE: &'static [u8] = include_bytes!({:?});\n",
                exe
            ));
            generated.push_str(&format!(
                "pub const ENGINE_SUPPORT: Option<(&'static str, &'static [u8])> = Some((\"7z.dll\", include_bytes!({:?})));\n",
                dll
            ));
        }
        "macos" => {
            let exe = engines.join("macos").join("7zz");
            if !exe.is_file() {
                panic!("{} is missing; run `npm run prepare:engine`", exe.display());
            }
            println!("cargo:rerun-if-changed={}", exe.display());
            generated.push_str(&format!(
                "pub const ENGINE_EXE: &'static [u8] = include_bytes!({:?});\n",
                exe
            ));
            generated.push_str("pub const ENGINE_SUPPORT: Option<(&'static str, &'static [u8])> = None;\n");
        }
        other => panic!("zip233 currently supports Windows and macOS targets, not {other}"),
    }

    let out = PathBuf::from(env::var_os("OUT_DIR").expect("out dir"));
    fs::write(out.join("zip233_assets.rs"), generated).expect("write generated asset map");
}
