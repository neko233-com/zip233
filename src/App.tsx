import { hasDesktopBridge, invoke } from "./bridge";
import {
  Archive,
  ArchiveRestore,
  Check,
  CheckCircle2,
  ChevronRight,
  CircleHelp,
  Clock3,
  FileArchive,
  FilePlus2,
  FileText,
  Folder,
  FolderOpen,
  HardDriveDownload,
  ListFilter,
  LoaderCircle,
  LockKeyhole,
  Plus,
  Search,
  Settings2,
  ShieldCheck,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DragEvent, ReactNode } from "react";
import type { ArchiveItem, ArchiveRecord, LaunchRequest, ShellIntegrationStatus, ToolResult } from "./types";

type View = "files" | "recent" | "completed" | "settings";
type FormatOption = {
  id: string;
  label: string;
  extension: string;
  family: string;
  canPassword?: boolean;
};

const formats: FormatOption[] = [
  { id: "7z", label: "7Z", extension: "7z", family: "zip233 · 高压缩率", canPassword: true },
  { id: "zip", label: "ZIP", extension: "zip", family: "跨平台通用", canPassword: true },
  { id: "tar", label: "TAR", extension: "tar", family: "无损归档" },
  { id: "tar.gz", label: "TAR.GZ", extension: "tar.gz", family: "Unix / Linux 常用" },
  { id: "tar.xz", label: "TAR.XZ", extension: "tar.xz", family: "高压缩率" },
  { id: "tar.bz2", label: "TAR.BZ2", extension: "tar.bz2", family: "兼容性好" },
  { id: "gzip", label: "GZIP", extension: "gz", family: "单文件压缩" },
  { id: "bzip2", label: "BZIP2", extension: "bz2", family: "单文件压缩" },
  { id: "xz", label: "XZ", extension: "xz", family: "单文件压缩" },
  { id: "wim", label: "WIM", extension: "wim", family: "Windows 映像" },
];

const storageKey = "zip233.recent.v1";
const isDesktop = hasDesktopBridge();

function readRecent(): ArchiveRecord[] {
  try {
    const value = JSON.parse(localStorage.getItem(storageKey) || "[]");
    return Array.isArray(value) ? value : [];
  } catch {
    return [];
  }
}

function pathName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path;
}

function fileFormat(name: string): string {
  const lower = name.toLowerCase();
  if (lower.endsWith(".tar.gz") || lower.endsWith(".tgz")) return "TGZ";
  if (lower.endsWith(".tar.xz") || lower.endsWith(".txz")) return "TXZ";
  if (lower.endsWith(".tar.bz2") || lower.endsWith(".tbz2")) return "TBZ2";
  const extension = lower.split(".").at(-1) ?? "file";
  return extension.toUpperCase();
}

function prettyBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let size = value / 1024;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size >= 100 ? size.toFixed(0) : size.toFixed(1)} ${units[unit]}`;
}

function formatDate(value: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(date);
}

function resultError(result: ToolResult): string {
  return result.stderr.trim().split(/\r?\n/).filter(Boolean).at(-1) ?? result.stdout.trim().split(/\r?\n/).filter(Boolean).at(-1) ?? "操作没有完成。";
}

function App() {
  const [view, setView] = useState<View>("files");
  const [recent, setRecent] = useState<ArchiveRecord[]>(readRecent);
  const [archivePath, setArchivePath] = useState<string | null>(null);
  const [items, setItems] = useState<ArchiveItem[]>([]);
  const [archivePassword, setArchivePassword] = useState("");
  const [passwordPrompt, setPasswordPrompt] = useState(false);
  const [loadingArchive, setLoadingArchive] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [sources, setSources] = useState<string[]>([]);
  const [createName, setCreateName] = useState("");
  const [createFormat, setCreateFormat] = useState("7z");
  const [compressionLevel, setCompressionLevel] = useState("5");
  const [createPassword, setCreatePassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<{ message: string; error?: boolean } | null>(null);
  const [query, setQuery] = useState("");
  const [shellMenu, setShellMenu] = useState(false);
  const [menuLoading, setMenuLoading] = useState(false);
  const [extractPassword, setExtractPassword] = useState("");
  const dragDepth = useRef(0);
  const [dragging, setDragging] = useState(false);

  const notify = useCallback((message: string, error = false) => {
    setToast({ message, error });
    window.setTimeout(() => setToast(null), 3600);
  }, []);

  const saveRecords = useCallback((records: ArchiveRecord[]) => {
    setRecent(records);
    localStorage.setItem(storageKey, JSON.stringify(records.slice(0, 100)));
  }, []);

  const pushRecord = useCallback((record: ArchiveRecord) => {
    setRecent((current) => {
      const next = [record, ...current.filter((entry) => entry.path !== record.path || entry.action !== record.action)].slice(0, 100);
      localStorage.setItem(storageKey, JSON.stringify(next));
      return next;
    });
  }, []);

  const addSourcePaths = useCallback((paths: string[]) => {
    const unique = [...new Set(paths)].filter(Boolean);
    if (unique.length === 0) return;
    setSources((current) => [...new Set([...current, ...unique])]);
    setCreateName((current) => {
      if (current) return current;
      const base = pathName(unique[0]).replace(/\.[^.]+$/, "") || "归档文件";
      return `${base}.7z`;
    });
    setShowCreate(true);
  }, []);

  const openArchive = useCallback(async (path: string, password?: string) => {
    if (!isDesktop) {
      setArchivePath(path);
      setItems([]);
      notify("归档预览需要在 zip233 桌面版中运行。", true);
      return;
    }
    setArchivePath(path);
    setItems([]);
    setArchivePassword(password ?? "");
    setLoadingArchive(true);
    try {
      const value = await invoke<ArchiveItem[]>("list_archive", { path, password: password || null });
      setItems(value);
      setPasswordPrompt(false);
      pushRecord({ path, name: pathName(path), format: fileFormat(path), size: 0, modified: new Date().toISOString(), action: "extract", result: "success" });
    } catch (error) {
      const message = String(error);
      if (!password && /password|wrong password|data error|headers error/i.test(message)) {
        setPasswordPrompt(true);
        return;
      }
      notify(message, true);
    } finally {
      setLoadingArchive(false);
    }
  }, [notify, pushRecord]);

  const chooseArchive = useCallback(async () => {
    if (!isDesktop) {
      notify("请在 zip233 桌面版中选择本地归档。", true);
      return;
    }
    try {
      const selected = await invoke<string | null>("pick_archive");
      if (selected) await openArchive(selected);
    } catch (error) {
      notify(String(error), true);
    }
  }, [notify, openArchive]);

  const chooseSources = useCallback(async () => {
    if (!isDesktop) {
      notify("请在 zip233 桌面版中选择本地文件。", true);
      return;
    }
    try {
      const selected = await invoke<string[]>("pick_sources");
      if (selected.length) addSourcePaths(selected);
    } catch (error) {
      notify(String(error), true);
    }
  }, [addSourcePaths, notify]);

  const chooseSourceFolder = useCallback(async () => {
    if (!isDesktop) {
      notify("请在 zip233 桌面版中选择本地文件夹。", true);
      return;
    }
    try {
      const selected = await invoke<string | null>("pick_source_folder");
      if (selected) addSourcePaths([selected]);
    } catch (error) {
      notify(String(error), true);
    }
  }, [addSourcePaths, notify]);

  const startCreate = useCallback(() => {
    setSources([]);
    setCreateName("");
    setCreatePassword("");
    setCreateFormat("7z");
    setShowCreate(true);
    void chooseSources();
  }, [chooseSources]);

  const createArchive = useCallback(async () => {
    if (!isDesktop) {
      notify("压缩需要在 zip233 桌面版中运行。", true);
      return;
    }
    if (sources.length === 0) {
      notify("请先添加要压缩的文件或文件夹。", true);
      return;
    }
    const option = formats.find((item) => item.id === createFormat)!;
    const defaultName = createName.trim() || `归档文件.${option.extension}`;
    try {
      const target = await invoke<string | null>("save_archive", { defaultName, extension: option.extension });
      if (!target) return;
      setBusy(true);
      const result = await invoke<ToolResult>("create_archive", {
        paths: sources,
        target,
        format: createFormat,
        level: compressionLevel,
        password: createPassword || null,
      });
      if (!result.ok) throw new Error(resultError(result));
      pushRecord({ path: target, name: pathName(target), format: fileFormat(target), size: 0, modified: new Date().toISOString(), action: "create", result: "success" });
      setShowCreate(false);
      setSources([]);
      notify("压缩包已创建。zip233 已保留原始文件。 ");
    } catch (error) {
      notify(String(error), true);
    } finally {
      setBusy(false);
    }
  }, [compressionLevel, createFormat, createName, createPassword, notify, pushRecord, sources]);

  const extractCurrent = useCallback(async () => {
    if (!archivePath || !isDesktop) return;
    try {
      const outputDir = await invoke<string | null>("pick_output_folder");
      if (!outputDir) return;
      setBusy(true);
      const result = await invoke<ToolResult>("extract_archive", {
        path: archivePath,
        outputDir,
        password: extractPassword || archivePassword || null,
      });
      if (!result.ok) throw new Error(resultError(result));
      pushRecord({ path: archivePath, name: pathName(archivePath), format: fileFormat(archivePath), size: 0, modified: new Date().toISOString(), action: "extract", result: "success" });
      setArchivePath(null);
      notify("解压完成。文件已保存到所选文件夹。 ");
    } catch (error) {
      notify(String(error), true);
    } finally {
      setBusy(false);
    }
  }, [archivePassword, archivePath, extractPassword, notify, pushRecord]);

  const testCurrent = useCallback(async () => {
    if (!archivePath || !isDesktop) return;
    setBusy(true);
    try {
      const result = await invoke<ToolResult>("test_archive", { path: archivePath, password: extractPassword || archivePassword || null });
      if (!result.ok) throw new Error(resultError(result));
      pushRecord({ path: archivePath, name: pathName(archivePath), format: fileFormat(archivePath), size: 0, modified: new Date().toISOString(), action: "test", result: "success" });
      notify("完整性检查通过。归档数据可正常读取。 ");
    } catch (error) {
      notify(String(error), true);
    } finally {
      setBusy(false);
    }
  }, [archivePassword, archivePath, extractPassword, notify, pushRecord]);

  const changeShellMenu = useCallback(async (register: boolean) => {
    setMenuLoading(true);
    try {
      const status = await invoke<ShellIntegrationStatus>(register ? "repair_shell_integration" : "unregister_shell_integration");
      setShellMenu(status.enabled && status.healthy);
      notify(register ? "zip233 右键菜单已修复，自启动检查已开启。" : "zip233 右键菜单已移除。 ");
    } catch (error) {
      notify(String(error), true);
    } finally {
      setMenuLoading(false);
    }
  }, [notify]);

  useEffect(() => {
    if (!isDesktop) return;
    void invoke<ShellIntegrationStatus>("shell_integration_status").then((status) => setShellMenu(status.enabled && status.healthy)).catch(() => undefined);
    void invoke<LaunchRequest | null>("get_launch_request").then((request) => {
      if (!request?.path) return;
      if (request.action === "compress" || request.action === "compress-here") addSourcePaths([request.path]);
      else if (request.action === "open" || request.action === "extract") void openArchive(request.path);
    }).catch(() => undefined);
    const previousPush = window.__RDESKTOP_PUSH__;
    window.__RDESKTOP_PUSH__ = (message) => {
      if (message.cmd !== "rdesktop.fileDrop") {
        previousPush?.(message);
        return;
      }
      const phase = message.payload?.phase;
      if (phase === "enter") setDragging(true);
      if (phase === "leave") setDragging(false);
      if (phase === "drop") {
        setDragging(false);
        const paths = message.payload?.paths;
        if (Array.isArray(paths)) addSourcePaths(paths.filter((path): path is string => typeof path === "string"));
      }
    };
    return () => { window.__RDESKTOP_PUSH__ = previousPush; };
  }, [addSourcePaths, openArchive]);

  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "o") {
        event.preventDefault();
        void chooseArchive();
      }
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "n") {
        event.preventDefault();
        startCreate();
      }
      if (event.key === "Escape") {
        setArchivePath(null);
        setShowCreate(false);
        setPasswordPrompt(false);
      }
    };
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [chooseArchive, startCreate]);

  const filteredRecent = useMemo(() => {
    let rows = recent;
    if (view === "completed") rows = rows.filter((row) => row.result === "success");
    if (view === "recent") rows = [...rows].sort((a, b) => b.modified.localeCompare(a.modified));
    if (query.trim()) rows = rows.filter((row) => row.name.toLowerCase().includes(query.trim().toLowerCase()));
    return rows;
  }, [query, recent, view]);

  const titles: Record<View, { title: string; subtitle: string }> = {
    files: { title: "文件归档", subtitle: "压缩、解压与管理本地文件。" },
    recent: { title: "最近打开", subtitle: "继续处理你最近使用过的归档。" },
    completed: { title: "处理记录", subtitle: "查看 zip233 完成的压缩、解压与检查。" },
    settings: { title: "设置", subtitle: "管理 zip233 与桌面系统的集成方式。" },
  };

  function onDrop(event: DragEvent) {
    event.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    if (!isDesktop) {
      notify("请把文件拖入已安装的 zip233 桌面应用。", true);
      return;
    }
    const paths = Array.from(event.dataTransfer.files).map((file) => (file as File & { path?: string }).path).filter((value): value is string => Boolean(value));
    if (paths.length) addSourcePaths(paths);
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <button className="brand" onClick={() => setView("files")} aria-label="zip233 文件归档">
          <span className="brand-icon"><Archive size={20} strokeWidth={2.3} /></span>
          <span>zip233</span>
        </button>
        <nav className="primary-nav" aria-label="主导航">
          <NavButton active={view === "files"} icon={<Folder size={19} />} label="文件" onClick={() => setView("files")} />
          <NavButton active={view === "recent"} icon={<Clock3 size={19} />} label="最近" onClick={() => setView("recent")} />
          <NavButton active={view === "completed"} icon={<CheckCircle2 size={19} />} label="已完成" onClick={() => setView("completed")} />
        </nav>
        <div className="sidebar-bottom">
          <div className="engine-line"><span className="engine-dot" />7-Zip 引擎已就绪</div>
          <button className={`nav-item ${view === "settings" ? "active" : ""}`} onClick={() => setView("settings")}>
            <Settings2 size={18} /><span>设置</span>
          </button>
          <div className="sidebar-version">zip233 <span>0.1.0</span></div>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <div className="breadcrumb"><span>zip233</span><ChevronRight size={14} /><span>{titles[view].title}</span></div>
          <div className="topbar-tools">
            <button className="icon-button" title="搜索归档" onClick={() => document.getElementById("archive-search")?.focus()}><Search size={17} /></button>
            <span className="topbar-separator" />
            <button className="help-button" title="支持格式" onClick={() => setView("settings")}><CircleHelp size={17} /></button>
          </div>
        </header>

        <div className="content-scroll">
          {view === "settings" ? (
            <SettingsView shellMenu={shellMenu} loading={menuLoading} onToggle={changeShellMenu} />
          ) : (
            <>
              <section className="page-heading">
                <div>
                  <h1>{titles[view].title}</h1>
                  <p>{titles[view].subtitle}</p>
                </div>
                {view === "files" && <div className="heading-actions">
                  <button className="button button-primary" onClick={startCreate}><FilePlus2 size={18} />新建压缩包</button>
                  <button className="button button-secondary" onClick={chooseArchive}><FolderOpen size={18} />打开文件</button>
                </div>}
              </section>

              {view === "files" && <section
                className={`drop-zone ${dragging ? "drop-active" : ""}`}
                onDragEnter={(event) => { event.preventDefault(); dragDepth.current += 1; setDragging(true); }}
                onDragOver={(event) => event.preventDefault()}
                onDragLeave={(event) => { event.preventDefault(); dragDepth.current -= 1; if (dragDepth.current <= 0) { dragDepth.current = 0; setDragging(false); } }}
                onDrop={onDrop}
              >
                <span className="drop-icon"><Upload size={28} strokeWidth={1.65} /></span>
                <strong>{dragging ? "松开鼠标，添加文件" : "将文件拖到这里"}</strong>
                <button className="drop-link" onClick={chooseSources}>或从设备中选择文件</button>
                <span className="drop-caption">支持创建压缩包，也可以拖入已有归档直接打开</span>
              </section>}

              <section className={`recent-section ${view !== "files" ? "full-history" : ""}`}>
                <div className="section-header">
                  <h2>{view === "files" ? "最近的压缩包" : view === "recent" ? "最近使用" : "全部处理记录"}</h2>
                  <div className="section-actions">
                    <label className="search-box">
                      <Search size={15} />
                      <input id="archive-search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索名称" />
                    </label>
                    {view === "files" && <button className="text-link" onClick={() => setView("recent")}>查看全部<ChevronRight size={15} /></button>}
                    {recent.length > 0 && view !== "files" && <button className="text-link clear-link" onClick={() => { saveRecords([]); notify("本机处理记录已清除。 "); }}><Trash2 size={14} />清空记录</button>}
                  </div>
                </div>

                {filteredRecent.length === 0 ? (
                  <div className="empty-recent">
                    <span className="empty-icon"><ArchiveRestore size={19} /></span>
                    <div><strong>{query ? "没有匹配的归档" : "还没有处理记录"}</strong><p>{query ? "试试其他文件名。" : "完成的压缩、解压与检查会显示在这里。"}</p></div>
                  </div>
                ) : (
                  <div className="archive-table-wrap">
                    <table className="archive-table">
                      <thead><tr><th>名称</th><th>修改日期</th><th>大小</th><th>类型</th><th aria-label="操作" /></tr></thead>
                      <tbody>{filteredRecent.slice(0, view === "files" ? 5 : 100).map((row) => (
                        <tr key={`${row.path}-${row.action}`} onDoubleClick={() => void openArchive(row.path)}>
                          <td className="name-cell"><span className={`format-icon format-${row.format.toLowerCase()}`}><FileArchive size={17} /></span><button className="archive-name" onClick={() => void openArchive(row.path)} title={row.path}>{row.name}</button></td>
                          <td className="date-cell">{formatDate(row.modified)}</td>
                          <td>{row.size ? prettyBytes(row.size) : "—"}</td>
                          <td><span className="type-cell">{row.format} 归档</span></td>
                          <td><button className="row-open" aria-label={`打开 ${row.name}`} onClick={() => void openArchive(row.path)}><ChevronRight size={17} /></button></td>
                        </tr>
                      ))}</tbody>
                    </table>
                  </div>
                )}
              </section>

              {view === "files" && <section className="format-strip">
                <div className="format-strip-title"><ShieldCheck size={16} /><span>广泛格式支持</span></div>
                <div className="format-list"><span>7Z</span><span>ZIP</span><span>RAR</span><span>TAR</span><span>GZ</span><span>XZ</span><span>ISO</span><span>DMG</span><span>WIM</span><button onClick={() => setView("settings")}>全部格式<ChevronRight size={13} /></button></div>
              </section>}
            </>
          )}
        </div>
      </main>

      {showCreate && <CreateDialog
        sources={sources}
        name={createName}
        format={createFormat}
        level={compressionLevel}
        password={createPassword}
        busy={busy}
        onChangeName={setCreateName}
        onChangeFormat={(id) => { setCreateFormat(id); const next = formats.find((item) => item.id === id)!; setCreateName((current) => current.replace(/\.[^.]+$/, `.${next.extension}`)); }}
        onChangeLevel={setCompressionLevel}
        onChangePassword={setCreatePassword}
        onAdd={chooseSources}
        onAddFolder={chooseSourceFolder}
        onRemove={(path) => setSources((current) => current.filter((item) => item !== path))}
        onClose={() => setShowCreate(false)}
        onSubmit={createArchive}
      />}

      {archivePath && <ArchiveDialog
        path={archivePath}
        items={items}
        password={extractPassword}
        loading={loadingArchive}
        busy={busy}
        onPassword={setExtractPassword}
        onClose={() => { setArchivePath(null); setPasswordPrompt(false); }}
        onExtract={extractCurrent}
        onTest={testCurrent}
        passwordPrompt={passwordPrompt}
        onUnlock={() => void openArchive(archivePath, extractPassword)}
      />}

      {toast && <div className={`toast ${toast.error ? "toast-error" : ""}`}><span className="toast-mark">{toast.error ? <X size={15} /> : <Check size={15} />}</span>{toast.message}</div>}
    </div>
  );
}

function NavButton({ active, icon, label, onClick }: { active: boolean; icon: ReactNode; label: string; onClick: () => void }) {
  return <button className={`nav-item ${active ? "active" : ""}`} onClick={onClick}>{icon}<span>{label}</span></button>;
}

function SettingsView({ shellMenu, loading, onToggle }: { shellMenu: boolean; loading: boolean; onToggle: (register: boolean) => void }) {
  const windows = typeof navigator !== "undefined" && /windows/i.test(navigator.userAgent);
  return <section className="settings-page">
    <div className="settings-group">
      <div className="settings-group-heading"><h2>系统集成</h2><p>让 zip233 出现在 Windows 文件资源管理器的右键菜单中。</p></div>
      <div className="setting-row">
        <span className="setting-icon"><ListFilter size={18} /></span>
        <div className="setting-copy"><strong>Windows 右键菜单</strong><p>{windows ? "添加打开归档、压缩文件夹和在当前目录压缩的快捷操作。" : "此功能仅适用于 Windows。"}</p></div>
        <div className="setting-action">
          <span className={`setting-status ${shellMenu ? "status-on" : ""}`}><i />{shellMenu ? "已注册 · 自修复开启" : "未注册"}</span>
          <div className="setting-buttons">
            <button className="button button-secondary button-small" disabled={!windows || loading} onClick={() => onToggle(true)}>
              {loading ? <LoaderCircle className="spin" size={15} /> : null}{shellMenu ? "立即修复" : "注册并开启自修复"}
            </button>
            {shellMenu && <button className="button button-quiet button-small" disabled={!windows || loading} onClick={() => onToggle(false)}>移除</button>}
          </div>
        </div>
      </div>
      <div className="setting-note"><ShieldCheck size={16} /><span>仅写入当前 Windows 用户的注册表，不需要管理员权限。每次启动会校验 zip233 菜单项，并修复丢失或指向旧安装路径的注册。</span></div>
    </div>

    <div className="settings-group formats-group">
      <div className="settings-group-heading"><h2>压缩格式</h2><p>官方 7-Zip 引擎提供广泛的读取和提取支持。</p></div>
      <div className="format-matrix">
        <div><span>可创建</span><strong>7z · zip · tar · tar.gz · tar.xz · tar.bz2 · gz · bz2 · xz · wim</strong></div>
        <div><span>常见可提取</span><strong>7z · zip · rar · tar · gz · bz2 · xz · zst · iso · dmg · cab · wim</strong></div>
        <div><span>其他可读取</span><strong>arj · cpio · chm · lzh · rpm · vhd · vhdx · vmdk · vdi · qcow2 · udf · xar</strong></div>
      </div>
      <p className="format-footnote">每种格式的加密、分卷和写入能力由其规范及归档引擎决定。部分格式只支持读取和提取。</p>
    </div>

    <div className="settings-group about-group">
      <div className="settings-group-heading"><h2>关于 zip233</h2></div>
      <div className="about-row"><span className="about-brand"><Archive size={18} />zip233</span><span>0.1.0</span><span className="about-engine"><span className="engine-dot" />7-Zip 26.04</span></div>
      <p className="format-footnote">跨 Windows 与 macOS 的桌面归档工具。归档在本机处理。</p>
    </div>
  </section>;
}

function CreateDialog({ sources, name, format, level, password, busy, onChangeName, onChangeFormat, onChangeLevel, onChangePassword, onAdd, onAddFolder, onRemove, onClose, onSubmit }: {
  sources: string[]; name: string; format: string; level: string; password: string; busy: boolean;
  onChangeName: (value: string) => void; onChangeFormat: (id: string) => void; onChangeLevel: (level: string) => void; onChangePassword: (password: string) => void;
  onAdd: () => void; onAddFolder: () => void; onRemove: (path: string) => void; onClose: () => void; onSubmit: () => void;
}) {
  const selected = formats.find((item) => item.id === format)!;
  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) onClose(); }}>
    <section className="dialog create-dialog" role="dialog" aria-modal="true" aria-labelledby="create-title">
      <header className="dialog-header"><div><span className="dialog-eyebrow">新建归档</span><h2 id="create-title">创建压缩包</h2></div><button className="icon-button dialog-close" onClick={onClose} disabled={busy} aria-label="关闭"><X size={19} /></button></header>
      <div className="dialog-body">
        <label className="field-label">文件和文件夹<span>{sources.length} 项</span></label>
        <div className="source-list">
          {sources.length ? sources.map((source) => <div className="source-row" key={source}><Folder size={17} /><span title={source}>{pathName(source)}</span><button onClick={() => onRemove(source)} aria-label={`移除 ${pathName(source)}`}><X size={15} /></button></div>) : <div className="source-empty">选择要加入压缩包的文件或文件夹</div>}
          <div className="add-source-actions"><button className="add-source" onClick={onAdd}><Plus size={15} />添加文件</button><button className="add-source" onClick={onAddFolder}><FolderOpen size={15} />添加文件夹</button></div>
        </div>

        <div className="dialog-grid">
          <label className="field"><span>格式</span><select value={format} onChange={(event) => onChangeFormat(event.target.value)}>{formats.map((item) => <option key={item.id} value={item.id}>{item.label} — {item.family}</option>)}</select></label>
          <label className="field"><span>压缩等级</span><select value={level} onChange={(event) => onChangeLevel(event.target.value)}><option value="1">快速</option><option value="5">标准</option><option value="7">较高</option><option value="9">最高</option></select></label>
        </div>
        <label className="field"><span>文件名</span><div className="input-with-suffix"><input value={name.replace(/\.[^.]+$/, "")} onChange={(event) => onChangeName(`${event.target.value}.${selected.extension}`)} placeholder="归档文件" /><span>.{selected.extension}</span></div></label>
        {selected.canPassword && <label className="field"><span>密码 <em>可选</em></span><div className="input-icon-wrap"><LockKeyhole size={16} /><input type="password" value={password} onChange={(event) => onChangePassword(event.target.value)} placeholder="设置加密密码" autoComplete="new-password" /></div><small>7z 格式可加密文件名；压缩包中的文件将由密码保护。</small></label>}
      </div>
      <footer className="dialog-footer"><button className="button button-secondary" onClick={onClose} disabled={busy}>取消</button><button className="button button-primary" onClick={onSubmit} disabled={busy || !sources.length}>{busy ? <LoaderCircle className="spin" size={16} /> : <Archive size={16} />}{busy ? "正在压缩…" : "选择保存位置并压缩"}</button></footer>
    </section>
  </div>;
}

function ArchiveDialog({ path, items, password, loading, busy, passwordPrompt, onPassword, onClose, onExtract, onTest, onUnlock }: {
  path: string; items: ArchiveItem[]; password: string; loading: boolean; busy: boolean; passwordPrompt: boolean;
  onPassword: (value: string) => void; onClose: () => void; onExtract: () => void; onTest: () => void; onUnlock: () => void;
}) {
  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) onClose(); }}>
    <section className="dialog inspect-dialog" role="dialog" aria-modal="true" aria-labelledby="inspect-title">
      <header className="dialog-header"><div className="archive-title-wrap"><span className="archive-large-icon"><FileArchive size={19} /></span><div><span className="dialog-eyebrow">归档内容</span><h2 id="inspect-title" title={path}>{pathName(path)}</h2></div></div><button className="icon-button dialog-close" onClick={onClose} disabled={busy} aria-label="关闭"><X size={19} /></button></header>
      {passwordPrompt ? <div className="unlock-state"><div className="unlock-icon"><LockKeyhole size={21} /></div><h3>这个压缩包需要密码</h3><p>输入密码后即可查看文件并解压。</p><div className="unlock-form"><input type="password" value={password} onChange={(event) => onPassword(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") onUnlock(); }} autoFocus placeholder="输入密码" /><button className="button button-primary" onClick={onUnlock}>解锁</button></div></div> : <>
        <div className="archive-summary"><span>{loading ? "正在读取文件清单…" : `${items.length} 个项目`}</span><span>{items.reduce((total, item) => total + (item.size ?? 0), 0) ? prettyBytes(items.reduce((total, item) => total + (item.size ?? 0), 0)) : "归档内容"}</span></div>
        <div className="entry-list">
          {loading ? <div className="dialog-loading"><LoaderCircle className="spin" size={22} /><span>正在读取归档</span></div> : items.length ? items.slice(0, 300).map((item, index) => <div className="entry-row" key={`${item.name}-${index}`}><span className={`entry-icon ${item.is_directory ? "entry-folder" : ""}`}>{item.is_directory ? <Folder size={16} /> : <FileText size={16} />}</span><span className="entry-name" title={item.name}>{item.name}</span><span className="entry-size">{item.is_directory ? "—" : item.size != null ? prettyBytes(item.size) : "—"}</span><span className="entry-date">{item.modified ?? ""}</span></div>) : <div className="empty-entries"><FileArchive size={24} /><span>没有可显示的文件</span></div>}
        </div>
        <div className="password-inline"><LockKeyhole size={15} /><input type="password" value={password} onChange={(event) => onPassword(event.target.value)} placeholder="加密归档密码（如有）" autoComplete="current-password" /></div>
      </>}
      <footer className="dialog-footer inspect-footer"><button className="button button-secondary" onClick={onTest} disabled={busy || loading || passwordPrompt}><ShieldCheck size={16} />测试完整性</button><button className="button button-primary" onClick={onExtract} disabled={busy || loading || passwordPrompt}><HardDriveDownload size={16} />{busy ? "正在解压…" : "解压到…"}</button></footer>
    </section>
  </div>;
}

export default App;
