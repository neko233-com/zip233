# zip233

**zip233** is a Windows and macOS archive manager built with Rust and the [rdesktop](https://github.com/neko233-com/rdesktop) desktop framework. Its local archive engine is the official 7-Zip command-line engine; no Tauri runtime or preinstalled 7-Zip application is required.

## Features

- Open archives, inspect file lists, test integrity, and extract locally.
- Create 7z, ZIP, TAR, TAR.GZ, TAR.XZ, TAR.BZ2, GZIP, BZIP2, XZ, and WIM files.
- Read and extract the broad archive and disk-image formats supported by the bundled 7-Zip engine. Format capabilities vary; some formats are extract-only.
- On Windows, the installer creates per-user Explorer actions for opening supported archives, compressing a folder, and compressing into the current folder. Portable builds can register them from Settings.
- Registration is idempotent. If enabled, zip233 checks and repairs its registry entries at startup, including commands that point at an older installation path. Repair uses only zip233-owned keys under the current user and does not require administrator access.
- The archive engine and frontend assets are embedded in the application. At startup zip233 checks its per-user engine copy and restores missing or changed files from the embedded copy.

## Development

Requirements: Node.js, Rust, and the platform desktop toolchain (WebView2/MSVC/Windows SDK on Windows; Xcode Command Line Tools on macOS).

```sh
npm install
npm run dev                 # browser UI preview
npm run dev:native          # rdesktop native window with Vite hot reload
npm run build               # frontend and native release binary
```

The build downloads the official 7-Zip engine for the current target from the [ip7z/7zip release](https://github.com/ip7z/7zip/releases/tag/26.04), then embeds it in the binary. To create an installer/package, install the pinned rdesktop CLI and run `npm run package`:

```sh
cargo install --locked --git https://github.com/neko233-com/rdesktop.git --rev 5c1136851d1e73fde834b66250bb4f6ce4b43384 rdesktop-cli
npm run package
```

Windows packaging uses the rdesktop NSIS bundler and requires `makensis` on `PATH`. macOS packaging creates a native `.app` bundle and a compressed `.dmg` using `hdiutil`.

## Windows Explorer menu

In **Settings → Windows right-click menu**, choose **Register and enable self-repair**. Repeating this action safely updates zip233-owned registry values. When enabled, startup repairs missing entries or entries that point to a previous install location. Choose **Remove** to delete those menu items and turn off self-repair.

## Archive engine and license

zip233 embeds an unmodified official 7-Zip engine. The Windows build uses the matching `7z.exe` and `7z.dll`; macOS uses the official universal `7zz` binary. The upstream [license](https://github.com/ip7z/7zip/blob/26.04/DOC/License.txt) is included in [licenses/7zip.txt](licenses/7zip.txt). See [the upstream format matrix](https://www.7-zip.org/) for the engine's read/write capability details.
