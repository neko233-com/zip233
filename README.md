# zip233

**zip233** is a Windows and macOS archive manager built with Rust and the [rdesktop](https://github.com/neko233-com/rdesktop) desktop framework. Archive reading, extraction, integrity checking, and compression run in process through libraries compiled into zip233. Archive operations do not require users to install 7-Zip or another archive program.

## Features

- Open archives, inspect file lists, test integrity, and extract locally.
- Read and extract 7z, ZIP, RAR, TAR, gzip, bzip2, xz, JAR, and classic formats including ACE, ARJ, ARC/PAK, ZOO, LHA/LZH, UC2, SQ/SQ2, SQZ, HYP, ICE, and Pack-Ice.
- Create 7z, ZIP, TAR, TAR.GZ, TAR.XZ, TAR.BZ2, GZIP, BZIP2, and XZ archives.
- Read encrypted 7z, ZIP, RAR, and encrypted legacy formats supported by the reader. Create AES-256 encrypted 7z and ZIP archives. ZIP encryption protects file contents; ZIP names remain visible by format design. 7z can encrypt names.
- ZIP reading includes Deflate, Deflate64, BZip2, Zstandard, LZMA, PPMd, and XZ compression methods.
- Extraction rejects absolute paths and directory traversal entries.
- Windows installs include idempotent Explorer actions: direct ZIP compression without opening the UI, advanced compression in zip233, and opening archives. Startup self-repairs the registrations.

Format support is explicit. No implementation can promise support for every proprietary or undocumented format. Specialized disk images and future formats are not currently claimed as supported.

## Development

Requirements: Node.js, Rust, and the platform desktop toolchain (WebView2/MSVC/Windows SDK on Windows; Xcode Command Line Tools on macOS).

```sh
npm install
npm run dev                 # browser UI preview
npm run dev:native          # rdesktop native window with Vite hot reload
npm run build               # frontend, GUI, and CLI release binaries
npm run build:frontend      # required before cargo test
cargo test --lib            # round-trip all creator formats and check encryption/path safety
```

To create installers and the macOS app bundle, install the pinned rdesktop CLI and run `npm run package`:

```sh
cargo install --locked --git https://github.com/neko233-com/rdesktop.git --rev 46b31da1c5b84441816fe79868658d38e88a1d5d rdesktop-cli
npm run package
```

Windows packaging produces both a per-user `.msi` package and a per-user NSIS setup executable. It requires WiX Toolset 4 or newer and `makensis` on `PATH`. Both installers register the same Explorer actions and enable startup self-repair. macOS packaging creates a native `.app` bundle containing the GUI and CLI, plus a compressed `.dmg` using `hdiutil`.

Pushing a `v*` tag runs `.github/workflows/release.yml`, builds Windows and macOS packages, and publishes the installers as GitHub Release assets. The updater looks for the MSI on Windows and DMG on macOS.

## Command line

The `zip233-cli` executable is installed beside the GUI. It provides localized help, readable tables, and JSON output for scripts:

```sh
zip233-cli --help
zip233-cli formats --json
zip233-cli list archive.zip
zip233-cli test archive.7z --ask-password
zip233-cli extract archive.rar --output ./unpacked --password-stdin
zip233-cli create backup.7z ./project --ask-password
zip233-cli create backup.zip ./project --format zip --ask-password
```

Use `--ask-password` for hidden interactive input, or pipe one password line with `--password-stdin`. Passwords are not accepted as command-line text, avoiding shell-history and process-list exposure.

## Updates

In **Settings → Software updates**, use **Check for updates** to check manually. Automatic checking and downloading are off by default. Enabling them checks on launch, downloads an available package, and asks before opening its installer. Downloads try GitHub first and then configured-in-app mirror endpoints; all bytes must match the SHA-256 digest from GitHub's official Release API before zip233 marks an update ready.

## Windows Explorer menu

In **Settings → Windows right-click menu**, choose **Register and enable self-repair**. Folder and directory-background menus each expose two compression actions: **Directly compress to ZIP** runs silently and creates a non-overwriting ZIP beside the selected folder or inside the current directory; **zip233 Advanced Compression** opens the app with the folder selected. Repeating registration safely updates zip233-owned registry values. When enabled, startup repairs missing entries or entries that point at a previous install location. Choose **Remove** to delete those menu items and turn off self-repair.

## Built-in archive support

The reader uses [unarc-rs](https://github.com/mkrueger/unarc-rs) for 7z, ZIP, RAR, TAR, JAR, and a broad set of legacy formats. ZIP, 7z, TAR, gzip, bzip2, and xz creation is handled in process by Rust libraries. RAR and legacy formats are read/extract only. The CLI `formats` command prints the supported reader and creator lists. Encrypted archives require the correct password; zip233 does not attempt password cracking.
