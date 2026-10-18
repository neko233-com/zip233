import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const viteUrl = "http://127.0.0.1:1420/";
let vite;
let ownsVite = false;
try {
  const response = await fetch(viteUrl);
  if (!response.ok) throw new Error("Vite is not ready");
} catch {
  vite = spawn(process.execPath, [path.join(root, "node_modules", "vite", "bin", "vite.js"), "--host", "127.0.0.1"], {
    cwd: root,
    stdio: "inherit",
    env: process.env,
  });
  ownsVite = true;
  let ready = false;
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (vite.exitCode !== null) throw new Error("Vite stopped before it became ready");
    try {
      const response = await fetch(viteUrl);
      if (response.ok) { ready = true; break; }
    } catch {}
    await delay(250);
  }
  if (!ready) throw new Error("Vite did not become ready at http://127.0.0.1:1420");
}

const app = spawn("cargo", ["run"], {
  cwd: root,
  stdio: "inherit",
  env: { ...process.env, RDESKTOP_DEV_URL: viteUrl },
});

const stopVite = () => {
  if (ownsVite && vite && vite.exitCode === null) vite.kill();
};
process.on("SIGINT", () => { app.kill("SIGINT"); stopVite(); });
process.on("SIGTERM", () => { app.kill("SIGTERM"); stopVite(); });

const code = await new Promise((resolve) => app.once("exit", (status) => resolve(status ?? 1)));
stopVite();
process.exitCode = code;
