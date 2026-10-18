# zip233

**zip233** is a Windows and macOS archive manager built with Rust and the [rdesktop](https://github.com/neko233-com/rdesktop) desktop framework. Archive reading, extraction, integrity checking, and compression run in process through libraries compiled into zip233. It does not download, bundle, or launch the 7-Zip executable or depend on an installed archive utility.

## Features

- Open archives, inspect file lists, test integrity, and extract locally.
- Read and extract 7z, ZIP, RAR, TAR, gzip, bzip2, xz, and many classic formats including ACE, ARJ, ARC/PAK, ZOO, LHA/LZH, UC2, SQ/SQZ, ICE, and Pack-Ice.
- Create 7z, ZIP, TAR, TAR.GZ, TAR.XZ, TAR.BZ2, GZIP, BZIP2, and XZ archives.
- Formats and capabilities are implemented in-process. Archive format coverage is explicit; no program can promise support for literally every proprietary or undocumented format.
- Extraction rejects absolute paths and directory traversal entries.
- On Windows, the installer creates per-user Explorer actions for opening supported archives, compressing a folder, and compressing into the current folder. Portable builds can register them from Settings.
- Registration is idempotent. If enabled, zip233 checks and repairs its registry entries at startup, including commands that point at an older installation path. Repair uses only zip233-owned keys under the current user and does not require administrator access.

## Development

Requirements: Node.js, Rust, and the platform desktop toolchain (WebView2/MSVC/Windows SDK on Windows; Xcode Command Line Tools on macOS).

```sh
npm install
npm run dev                 # browser UI preview
npm run dev:native          # rdesktop native window with Vite hot reload
npm run build               # frontend and native release binary
```

To create an installer/package, install the pinned rdesktop CLI and run `npm run package`:

```sh
cargo install --locked --git https://github.com/neko233-com/rdesktop.git --rev 46b31da1c5b84441816fe79868658d38e88a1d5d rdesktop-cli
npm run package
```

Windows packaging uses the rdesktop NSIS bundler and requires `makensis` on `PATH`. macOS packaging creates a native `.app` bundle and a compressed `.dmg` using `hdiutil`.

## Windows Explorer menu

In **Settings → Windows right-click menu**, choose **Register and enable self-repair**. Repeating this action safely updates zip233-owned registry values. When enabled, startup repairs missing entries or entries that point to a previous install location. Choose **Remove** to delete those menu items and turn off self-repair.

## Built-in archive support

The archive engine is composed of Rust libraries and is compiled into zip233. There is no external 7-Zip process, archive DLL download, or per-user engine installation to repair. Rust dependencies are resolved at build time; the shipped app performs archive work locally without requiring users to install an archive tool.

The reader uses [unarc-rs](https://github.com/mkrueger/unarc-rs) for 7z, ZIP, RAR, TAR, and a broad set of legacy formats. ZIP, 7z, TAR, gzip, bzip2, and xz creation is handled in process by Rust libraries. RAR and legacy formats are read/extract only. Some specialized disk images and proprietary formats are not currently supported.
