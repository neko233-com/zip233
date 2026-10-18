import { chmod, copyFile, mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const target = process.argv[2] ?? spawnSync("rustc", ["--print", "host-tuple"], { encoding: "utf8" }).stdout.trim();
const tag = process.env.ZIP233_7ZIP_TAG ?? "26.04";
const version = tag.replaceAll(".", "");
const engineRoot = path.join(root, "resources", "engines");
const windows = target.includes("windows");
const mac = target.includes("apple-darwin");

if (!windows && !mac) {
  throw new Error(`当前构建脚本只提供 Windows/macOS 引擎（目标：${target}）`);
}
if (windows && !target.startsWith("x86_64-")) {
  throw new Error(`当前发行包尚未配置 Windows ARM64 引擎（目标：${target}）`);
}

const binaryDir = path.join(engineRoot, windows ? "windows" : "macos");
const destination = path.join(binaryDir, windows ? "7z.exe" : "7zz");
const supportDestination = windows ? path.join(binaryDir, "7z.dll") : undefined;
await mkdir(binaryDir, { recursive: true });
try {
  const { stat } = await import("node:fs/promises");
  const ready = (await stat(destination)).isFile() && (!supportDestination || (await stat(supportDestination)).isFile());
  if (ready) process.exit(0);
} catch {}

const scratch = await mkdtemp(path.join(tmpdir(), "zip233-engine-"));
async function download(name, to) {
  const url = `https://github.com/ip7z/7zip/releases/download/${encodeURIComponent(tag)}/${encodeURIComponent(name)}`;
  const response = await fetch(url, { headers: { "User-Agent": "zip233-build" } });
  if (!response.ok) throw new Error(`下载 ${name} 失败：HTTP ${response.status}`);
  await writeFile(to, Buffer.from(await response.arrayBuffer()));
}

async function findFile(directory, filename) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const fullPath = path.join(directory, entry.name);
    if (entry.isFile() && entry.name.toLowerCase() === filename.toLowerCase()) return fullPath;
    if (entry.isDirectory()) {
      const found = await findFile(fullPath, filename);
      if (found) return found;
    }
  }
  return undefined;
}

try {
  let source;
  if (windows) {
    const bootstrap = path.join(scratch, "7zr.exe");
    const installer = path.join(scratch, `7z${version}-x64.exe`);
    const unpacked = path.join(scratch, "unpacked");
    await mkdir(unpacked);
    await Promise.all([download("7zr.exe", bootstrap), download(`7z${version}-x64.exe`, installer)]);
    const result = spawnSync(bootstrap, ["x", installer, `-o${unpacked}`, "-y"], { encoding: "utf8" });
    if (result.status !== 0) throw new Error(`解包官方 7-Zip 工具失败：${result.stderr || result.stdout}`);
    source = await findFile(unpacked, "7z.exe");
    const dll = await findFile(unpacked, "7z.dll");
    if (!source || !dll) throw new Error("7-Zip 官方安装包中没有找到 7z.exe 与配套的 7z.dll");
    await copyFile(dll, supportDestination);
  } else {
    const archive = path.join(scratch, `7z${version}-mac.tar.xz`);
    const unpacked = path.join(scratch, "unpacked");
    await mkdir(unpacked);
    await download(`7z${version}-mac.tar.xz`, archive);
    const result = spawnSync("tar", ["-xf", archive, "-C", unpacked], { encoding: "utf8" });
    if (result.status !== 0) throw new Error(`解包官方 macOS 工具失败：${result.stderr || result.stdout}`);
    source = await findFile(unpacked, "7zz");
    if (!source) throw new Error("7-Zip 官方 macOS 包中没有找到 7zz");
    await chmod(source, 0o755);
  }
  if (!source) throw new Error("没有找到适用于当前平台的 7-Zip 引擎");
  await copyFile(source, destination);
  if (!windows) await chmod(destination, 0o755);
  console.log(`已准备 7-Zip ${tag} 引擎：${destination}`);
} finally {
  await rm(scratch, { recursive: true, force: true });
}
