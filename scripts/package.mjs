import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const archiveExtensions = ["7z", "zip", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz", "Z", "taz", "ace", "arj", "arc", "pak", "zoo", "ha", "jar", "j", "uc2", "sq", "sq2", "qqq", "sqz", "hyp", "lzh", "lha", "ice", "pi9"];
const legacyArchiveExtensions = ["zst", "lz", "lzma", "cab", "wim", "iso", "dmg", "rpm", "deb", "cpio", "chm", "vhd", "vhdx", "vmdk", "vdi", "qcow2", "udf", "xar", "apfs", "squashfs", "cramfs", "fat", "ntfs", "gpt", "mbr", "uefi", "ihex", "nsis", "msi", "hfs"];
const windows = process.platform === "win32";
const mac = process.platform === "darwin";
if (!windows && !mac) throw new Error("zip233 packages Windows and macOS builds on their native hosts.");
const packageInfo = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
const version = packageInfo.version;

const binary = path.join(root, "target", "release", windows ? "zip233.exe" : "zip233");
const cliBinary = path.join(root, "target", "release", windows ? "zip233-cli.exe" : "zip233-cli");
const icon = windows
  ? path.join(root, "resources", "icons", "zip233.ico")
  : await createMacIcon();
const tempProject = await mkdtemp(path.join(tmpdir(), "zip233-rdesktop-package-"));
const target = windows ? "nsis" : "app";
const config = `[app]\nidentifier = "com.neko233.zip233"\nname = "zip233"\nversion = "${version}"\n\n[renderer]\nkind = "webview"\ntrusted_origins = ["rdesktop://localhost"]\n\n[bundle]\nwindows_installer = "nsis"\nmacos_bundle_id = "com.neko233.zip233"\nmacos_dmg = false\nicon = ${JSON.stringify(icon)}\ncopyright = "neko233-com"\n`;

try {
  await writeFile(path.join(tempProject, "rdesktop.toml"), config, "utf8");
  const cli = process.env.ZIP233_RDESKTOP_CLI ?? "rdesktop";
  const bundle = spawnSync(cli, ["bundle", "--path", tempProject, "--target", target, "--binary", binary], {
    cwd: root,
    stdio: "inherit",
  });
  if (bundle.error?.code === "ENOENT") {
    throw new Error("找不到 rdesktop CLI。请先安装固定版本：cargo install --locked --git https://github.com/neko233-com/rdesktop.git --rev 46b31da1c5b84441816fe79868658d38e88a1d5d rdesktop-cli");
  }
  if (bundle.status !== 0) throw new Error(`rdesktop bundle failed with status ${bundle.status}`);

  if (windows) {
    await patchInstaller();
    await patchUninstaller();
    await buildMsi();
  }

  if (mac) {
    const appPath = path.join(root, "target", "release", "bundle", "macos", "zip233.app");
    const appCliPath = path.join(appPath, "Contents", "MacOS", "zip233-cli");
    await copyFile(cliBinary, appCliPath);
    await chmod(appCliPath, 0o755);
    const dmgPath = path.join(root, "target", "release", "bundle", "macos", `zip233-${version}.dmg`);
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
  const installerPath = path.join(outputDir, `zip233-${version}-setup.exe`);
  let source = stripBom(await readFile(scriptPath, "utf8"));
  const uninstallStart = source.lastIndexOf('Section "Uninstall"');
  const uninstallEnd = source.indexOf("SectionEnd", uninstallStart);
  if (uninstallStart < 0 || uninstallEnd < 0) throw new Error("rdesktop NSIS script did not contain its uninstall section");

  const cleanup = [
    ...[...archiveExtensions, ...legacyArchiveExtensions].map((extension) => `    DeleteRegKey HKCU "Software\\Classes\\SystemFileAssociations\\.${extension}\\shell\\zip233.open"`),
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\shell\\zip233.compress-zip"',
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\shell\\zip233.compress-advanced"',
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-zip-here"',
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-advanced-here"',
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\shell\\zip233.compress"',
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-here"',
    '    DeleteRegValue HKCU "Software\\neko233\\zip233" "ShellIntegrationEnabled"',
    '    RMDir /r "$LOCALAPPDATA\\zip233\\runtime"',
  ].join("\n") + "\n";
  source = source.slice(0, uninstallEnd) + cleanup + source.slice(uninstallEnd);
  source = source.replace('    RMDir "$INSTDIR"', '    Delete "$INSTDIR\\zip233-cli.exe"\n    RMDir "$INSTDIR"');
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

  const writes = [
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\shell\\zip233.compress"',
    '    DeleteRegKey HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-here"',
    '    WriteRegDWORD HKCU "Software\\neko233\\zip233" "ShellIntegrationEnabled" 1',
    ...archiveExtensions.flatMap((extension) => {
      const key = `Software\\Classes\\SystemFileAssociations\\.${extension}\\shell\\zip233.open`;
      return [
        `    WriteRegStr HKCU "${key}" "MUIVerb" "使用 zip233 打开"`,
        `    WriteRegStr HKCU "${key}" "Icon" "$INSTDIR\\zip233.exe"`,
        `    WriteRegStr HKCU "${key}\\command" "" '"$INSTDIR\\zip233.exe" open "%1"'`,
      ];
    }),
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress-zip" "MUIVerb" "直接压缩为 ZIP"',
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress-zip" "Icon" "$INSTDIR\\zip233.exe"',
    `    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress-zip\\command" "" '"$INSTDIR\\zip233.exe" compress-zip "%1"'`,
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress-advanced" "MUIVerb" "zip233 高级压缩…"',
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress-advanced" "Icon" "$INSTDIR\\zip233.exe"',
    `    WriteRegStr HKCU "Software\\Classes\\Directory\\shell\\zip233.compress-advanced\\command" "" '"$INSTDIR\\zip233.exe" compress-advanced "%1"'`,
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-zip-here" "MUIVerb" "直接压缩为 ZIP"',
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-zip-here" "Icon" "$INSTDIR\\zip233.exe"',
    `    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-zip-here\\command" "" '"$INSTDIR\\zip233.exe" compress-zip-here "%V"'`,
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-advanced-here" "MUIVerb" "zip233 高级压缩…"',
    '    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-advanced-here" "Icon" "$INSTDIR\\zip233.exe"',
    `    WriteRegStr HKCU "Software\\Classes\\Directory\\Background\\shell\\zip233.compress-advanced-here\\command" "" '"$INSTDIR\\zip233.exe" compress-advanced-here "%V"'`,
  ].join("\n") + "\n";
  source = source.slice(0, installEnd) + writes + source.slice(installEnd);
  const cliFile = `    File /oname=zip233-cli.exe "${cliBinary}"\n`;
  if (!source.includes('    File /oname=icon.ico')) throw new Error("rdesktop NSIS script did not contain its icon file entry");
  source = source.replace('    File /oname=icon.ico', `${cliFile}    File /oname=icon.ico`);
  await writeFile(scriptPath, `\uFEFF${source}`, "utf8");
}

async function buildMsi() {
  const template = await readFile(path.join(root, "installer", "zip233.wxs"), "utf8");
  const shellEntries = archiveExtensions.map((extension) => {
    const key = `Software\\Classes\\SystemFileAssociations\\.${extension}\\shell\\zip233.open`;
    return `      <RegistryKey Root="HKCU" Key="${key}">\n        <RegistryValue Name="MUIVerb" Type="string" Value="使用 zip233 打开" />\n        <RegistryValue Name="Icon" Type="string" Value="[#AppExe]" />\n        <RegistryKey Key="command"><RegistryValue Type="string" Value="&quot;[#AppExe]&quot; open &quot;%1&quot;" /></RegistryKey>\n      </RegistryKey>`;
  }).join("\n");
  const oldEntries = legacyArchiveExtensions.map((extension) => {
    const key = `Software\\Classes\\SystemFileAssociations\\.${extension}\\shell\\zip233.open`;
    return `      <RemoveRegistryKey Root="HKCU" Key="${key}" Action="removeOnInstall" />`;
  }).join("\n");
  const generated = template
    .replace("<!-- ZIP233_ARCHIVE_ENTRIES -->", shellEntries)
    .replace("<!-- ZIP233_LEGACY_CLEANUP -->", oldEntries);
  const msiDir = path.join(root, "target", "release", "bundle", "msi");
  await mkdir(msiDir, { recursive: true });
  const sourcePath = path.join(msiDir, "zip233.wxs");
  const outputPath = path.join(msiDir, `zip233-${packageInfo.version}-x64.msi`);
  await writeFile(sourcePath, generated, "utf8");
  run("wix", [
    "build", "-arch", "x64", "-d", "AppExe=" + binary, "-d", "CliExe=" + cliBinary,
    "-d", "IconFile=" + icon, "-d", "PackageVersion=" + packageInfo.version,
    "-o", outputPath, sourcePath,
  ]);
}

function stripBom(text) {
  return text.startsWith("\uFEFF") ? text.slice(1) : text;
}

async function createMacIcon() {
  const iconSource = path.join(root, "resources", "icons", "zip233-256.png");
  const iconSetTemp = await mkdtemp(path.join(tmpdir(), "zip233-iconset-"));
  const iconSet = path.join(iconSetTemp, "zip233.iconset");
  await mkdir(iconSet);
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
    await rm(iconSetTemp, { recursive: true, force: true });
  }
}

function run(command, args) {
  const result = spawnSync(command, args, { cwd: root, stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed with status ${result.status}`);
}
