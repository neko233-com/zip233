import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const windows = process.platform === "win32";
const mac = process.platform === "darwin";
if (!windows && !mac) throw new Error("zip233 packages Windows and macOS builds on their native hosts.");

const binary = path.join(root, "target", "release", windows ? "zip233.exe" : "zip233");
const icon = windows
  ? path.join(root, "resources", "icons", "zip233.ico")
  : await createMacIcon();
const tempProject = await mkdtemp(path.join(tmpdir(), "zip233-rdesktop-package-"));
const target = windows ? "nsis" : "app";
const config = `[app]\nidentifier = "com.neko233.zip233"\nname = "zip233"\nversion = "0.1.0"\n\n[renderer]\nkind = "webview"\ntrusted_origins = ["rdesktop://localhost"]\n\n[bundle]\nwindows_installer = "nsis"\nmacos_bundle_id = "com.neko233.zip233"\nmacos_dmg = false\nicon = ${JSON.stringify(icon)}\ncopyright = "neko233-com"\n`;

try {
  await writeFile(path.join(tempProject, "rdesktop.toml"), config, "utf8");
  const cli = process.env.ZIP233_RDESKTOP_CLI ?? "rdesktop";
  const bundle = spawnSync(cli, ["bundle", "--path", tempProject, "--target", target, "--binary", binary], {
    cwd: root,
    stdio: "inherit",
  });
  if (bundle.error?.code === "ENOENT") {
    throw new Error("找不到 rdesktop CLI。请先安装 rdesktop 0.1.15：cargo install --git https://github.com/neko233-com/rdesktop.git --tag v0.1.15 rdesktop-cli");
  }
  if (bundle.status !== 0) throw new Error(`rdesktop bundle failed with status ${bundle.status}`);

  if (windows) {
    await patchInstaller();
    await patchUninstaller();
  }

  if (mac) {
    const appPath = path.join(root, "target", "release", "bundle", "macos", "zip233.app");
    const dmgPath = path.join(root, "target", "release", "bundle", "macos", "zip233-0.1.0.dmg");
    await mkdir(path.dirname(dmgPath), { recursive: true });
    await rm(dmgPath, { force: true });
    run("hdiutil", ["create", "-volname", "zip233", "-srcfolder", appPath, "-ov", "-format", "UDZO", dmgPath]);
  }
} finally {
  await rm(tempProject, { recursive: true, force: true });
}

async function patchUninstaller() {
  const outputDir = path.join(root, "target", "release", "bundle", "nsis");
  const scriptPath = path.join(outputDir, "installer.nsi");
  const installerPath = path.join(outputDir, "zip233-0.1.0-setup.exe");
  let source = stripBom(await readFile(scriptPath, "utf8"));
  const uninstallStart = source.lastIndexOf('Section "Uninstall"');
  const uninstallEnd = source.indexOf("SectionEnd", uninstallStart);
  if (uninstallStart < 0 || uninstallEnd < 0) throw new Error("rdesktop NSIS script did not contain its uninstall section");

  const extensions = ["7z", "zip", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz", "zst", "lz", "lzma", "cab", "wim", "iso", "dmg", "rpm", "deb", "cpio", "arj", "lzh", "chm", "vhd", "vhdx", "vmdk", "vdi", "qcow2", "udf", "xar", "z", "apfs", "squashfs", "cramfs", "fat", "ntfs", "gpt", "mbr", "uefi", "ihex", "nsis", "msi", "hfs"];
  const cleanup = [
    ...extensions.map((extension) => `    DeleteRegKey HKCU "Software\\Classes\\SystemFileAssociations\\.${extension}\\shell\\zip233.open"`),
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\shell\\zip233.compress"',
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-here"',
    '    DeleteRegValue HKCU "Software\\neko233\\zip233" "ShellIntegrationEnabled"',
    '    RMDir /r "$LOCALAPPDATA\\zip233\\runtime"',
  ].join("\n") + "\n";
  source = source.slice(0, uninstallEnd) + cleanup + source.slice(uninstallEnd);
  await writeFile(scriptPath, `\uFEFF${source}`, "utf8");
  await rm(installerPath, { force: true });
  run("makensis", [scriptPath]);
}

async function patchInstaller() {
  const scriptPath = path.join(root, "target", "release", "bundle", "nsis", "installer.nsi");
  let source = stripBom(await readFile(scriptPath, "utf8"));
  const installStart = source.lastIndexOf('Section "Install"');
  const installEnd = source.indexOf("SectionEnd", installStart);
  if (installStart < 0 || installEnd < 0) throw new Error("rdesktop NSIS script did not contain its install section");

  const extensions = ["7z", "zip", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz", "zst", "lz", "lzma", "cab", "wim", "iso", "dmg", "rpm", "deb", "cpio", "arj", "lzh", "chm", "vhd", "vhdx", "vmdk", "vdi", "qcow2", "udf", "xar", "z", "apfs", "squashfs", "cramfs", "fat", "ntfs", "gpt", "mbr", "uefi", "ihex", "nsis", "msi", "hfs"];
  const writes = [
    '    WriteRegDWORD HKCU "Software\\neko233\\zip233" "ShellIntegrationEnabled" 1',
    ...extensions.flatMap((extension) => {
      const key = `Software\\Classes\\SystemFileAssociations\\.${extension}\\shell\\zip233.open`;
      return [
        `    WriteRegStr HKCU "${key}" "MUIVerb" "使用 zip233 打开"`,
        `    WriteRegStr HKCU "${key}" "Icon" "$INSTDIR\\zip233.exe"`,
        `    WriteRegStr HKCU "${key}\\command" "" "$"$INSTDIR\\zip233.exe$" open $"%1$""`,
      ];
    }),
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress" "MUIVerb" "压缩为 zip233.7z"',
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress" "Icon" "$INSTDIR\\zip233.exe"',
    `    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress\\command" "" "$"$INSTDIR\\zip233.exe$" compress $"%1$""`,
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-here" "MUIVerb" "在此处创建 zip233.7z"',
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-here" "Icon" "$INSTDIR\\zip233.exe"',
    `    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-here\\command" "" "$"$INSTDIR\\zip233.exe$" compress-here $"%V$""`,
  ].join("\n") + "\n";
  source = source.slice(0, installEnd) + writes + source.slice(installEnd);
  await writeFile(scriptPath, `\uFEFF${source}`, "utf8");
}

function stripBom(text) {
  return text.startsWith("\uFEFF") ? text.slice(1) : text;
}

async function createMacIcon() {
  const iconSource = path.join(root, "resources", "icons", "zip233-256.png");
  const iconSet = await mkdtemp(path.join(tmpdir(), "zip233-iconset-"));
  const output = path.join(root, "resources", "icons", "zip233.icns");
  try {
    const entries = [
      [16, "icon_16x16.png"], [32, "icon_16x16@2x.png"],
      [32, "icon_32x32.png"], [64, "icon_32x32@2x.png"],
      [128, "icon_128x128.png"], [256, "icon_128x128@2x.png"],
      [256, "icon_256x256.png"], [512, "icon_256x256@2x.png"],
      [512, "icon_512x512.png"], [1024, "icon_512x512@2x.png"],
    ];
    for (const [size, name] of entries) {
      run("sips", ["-s", "format", "png", "-z", String(size), String(size), iconSource, "--out", path.join(iconSet, name)]);
    }
    run("iconutil", ["-c", "icns", iconSet, "-o", output]);
    return output;
  } finally {
    await rm(iconSet, { recursive: true, force: true });
  }
}

function run(command, args) {
  const result = spawnSync(command, args, { cwd: root, stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed with status ${result.status}`);
}
