(() => {
  'use strict';

  const $ = (sel) => document.querySelector(sel);
  const $$ = (sel) => Array.from(document.querySelectorAll(sel));

  // ---------- 基础设施 ----------

  async function api(cmd, payload = {}) {
    const res = await fetch('/api/invoke', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ cmd, payload }),
    });
    let body;
    try { body = await res.json(); } catch (_) { throw new Error('服务返回格式错误'); }
    if (!res.ok || !body || !body.success) {
      const msg = body && body.data && body.data.error ? body.data.error : `请求失败 (${res.status})`;
      throw new Error(msg);
    }
    return body.data;
  }

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  let toastTimer = null;
  function toast(msg, type = '') {
    const t = $('#toast');
    t.textContent = msg;
    t.className = 'toast' + (type ? ' ' + type : '');
    t.hidden = false;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { t.hidden = true; }, 3600);
  }

  function confirmDialog(title, message) {
    return new Promise((resolve) => {
      $('#confirmTitle').textContent = title;
      $('#confirmMessage').textContent = message;
      const modal = $('#confirmModal');
      modal.hidden = false;
      const ok = $('#btnConfirmOk'), cancel = $('#btnConfirmCancel');
      function done(v) {
        modal.hidden = true;
        ok.onclick = null; cancel.onclick = null;
        resolve(v);
      }
      ok.onclick = () => done(true);
      cancel.onclick = () => done(false);
      const mask = modal.querySelector('.modal-mask');
      if (mask) mask.addEventListener('click', () => done(false), { once: true });
    });
  }

  const S = {
    get(key, def) {
      const v = localStorage.getItem('zip233.' + key);
      if (v === null) return def;
      try { return JSON.parse(v); } catch (_) { return def; }
    },
    set(key, val) { localStorage.setItem('zip233.' + key, JSON.stringify(val)); },
  };

  function basename(p) {
    const s = String(p || '').replace(/\\/g, '/');
    return s.split('/').filter(Boolean).pop() || s;
  }

  function fmtSize(n) {
    if (n === null || n === undefined) return '-';
    let b = Number(n);
    if (!isFinite(b)) return '-';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (b >= 1024 && i < units.length - 1) { b /= 1024; i++; }
    return (i === 0 ? b : b.toFixed(1)) + ' ' + units[i];
  }

  function fmtTime(ts) {
    const d = new Date(ts);
    if (isNaN(d.getTime())) return '-';
    return d.toLocaleString('zh-CN', { hour12: false });
  }

  function showModal(id) { document.getElementById(id).hidden = false; }
  function closeModal(id) { document.getElementById(id).hidden = true; }

  // ---------- 本地记录 ----------

  function loadList(key) {
    try { const v = JSON.parse(localStorage.getItem('zip233.' + key) || '[]'); return Array.isArray(v) ? v : []; }
    catch (_) { return []; }
  }
  function saveList(key, list) { localStorage.setItem('zip233.' + key, JSON.stringify(list.slice(0, 50))); }

  function addRecent(path) {
    const list = loadList('recent').filter((x) => x.path !== path);
    list.unshift({ path: path, time: Date.now() });
    saveList('recent', list);
    renderRecent();
  }

  function addCompleted(op, path) {
    const list = loadList('completed');
    list.unshift({ op: op, path: path, time: Date.now() });
    saveList('completed', list);
    renderCompleted();
  }

  function renderRecent() {
    const tbody = $('#recentTableBody');
    tbody.innerHTML = '';
    const list = loadList('recent');
    if (!list.length) {
      const tr = el('tr'), td = el('td', 'empty', '暂无最近记录');
      td.colSpan = 4; tr.appendChild(td); tbody.appendChild(tr);
      return;
    }
    list.forEach((item) => {
      const tr = el('tr');
      const name = el('td', '', basename(item.path));
      const path = el('td', 'path', item.path);
      path.title = item.path;
      const time = el('td', 'muted', fmtTime(item.time));
      const ops = el('td');
      const open = el('button', 'link', '打开');
      open.onclick = () => openArchive(item.path);
      const del = el('button', 'link danger', '移除');
      del.onclick = () => {
        saveList('recent', loadList('recent').filter((x) => x.path !== item.path));
        renderRecent();
      };
      ops.appendChild(open); ops.appendChild(del);
      tr.appendChild(name); tr.appendChild(path); tr.appendChild(time); tr.appendChild(ops);
      tbody.appendChild(tr);
    });
  }

  function renderCompleted() {
    const tbody = $('#completedTableBody');
    tbody.innerHTML = '';
    const list = loadList('completed');
    if (!list.length) {
      const tr = el('tr'), td = el('td', 'empty', '暂无已完成记录');
      td.colSpan = 4; tr.appendChild(td); tbody.appendChild(tr);
      return;
    }
    list.forEach((item) => {
      const tr = el('tr');
      tr.appendChild(el('td', '', item.op || '操作'));
      tr.appendChild(el('td', '', basename(item.path)));
      const path = el('td', 'path', item.path);
      path.title = item.path;
      tr.appendChild(path);
      tr.appendChild(el('td', 'muted', fmtTime(item.time)));
      tbody.appendChild(tr);
    });
  }

  // ---------- 视图切换 ----------

  const TITLES = { files: '文件', recent: '最近', completed: '已完成', settings: '设置' };
  function switchView(name) {
    $$('.nav-item').forEach((b) => b.classList.toggle('active', b.dataset.view === name));
    $$('.view').forEach((v) => { v.hidden = v.id !== 'view-' + name; });
    $('#viewTitle').textContent = TITLES[name] || '文件';
  }

  // ---------- 归档操作 ----------

  let currentArchivePath = null;
  let currentPassword = '';

  async function openArchive(presetPath, password, retried) {
    let path = presetPath;
    if (!path) {
      try { path = await api('pick_archive'); } catch (e) { toast('选择失败：' + e.message, 'error'); return; }
    }
    if (!path) return;
    const pw = password || '';
    let items;
    try {
      items = await api('list_archive', { path: path, password: pw });
    } catch (e) {
      if (!retried) {
        const input = window.prompt('无法读取该归档，若已加密请输入密码（留空取消）：', '');
        if (input !== null && input !== '') return openArchive(path, input, true);
      }
      toast('打开失败：' + e.message, 'error');
      return;
    }
    currentArchivePath = path;
    currentPassword = pw;
    $('#archivePathLabel').textContent = path;
    renderArchiveList(items || []);
    showModal('viewModal');
    addRecent(path);
  }

  function renderArchiveList(items) {
    const tbody = $('#archiveList');
    tbody.innerHTML = '';
    if (!items.length) {
      const tr = el('tr'), td = el('td', 'empty', '压缩包为空');
      td.colSpan = 5; tr.appendChild(td); tbody.appendChild(tr);
      return;
    }
    items.forEach((it) => {
      const tr = el('tr');
      tr.appendChild(el('td', '', it.name));
      tr.appendChild(el('td', 'muted', it.is_directory ? '文件夹' : '文件'));
      tr.appendChild(el('td', '', it.is_directory ? '-' : fmtSize(it.size)));
      tr.appendChild(el('td', '', it.is_directory ? '-' : fmtSize(it.packed_size)));
      tr.appendChild(el('td', 'muted', it.modified || '-'));
      tbody.appendChild(tr);
    });
  }

  async function testArchive() {
    if (!currentArchivePath) return;
    try {
      const res = await api('test_archive', { path: currentArchivePath, password: currentPassword });
      if (res && res.ok) toast('测试通过：归档完整', 'success');
      else toast('测试失败：' + ((res && res.stderr) || '发现损坏'), 'error');
    } catch (e) { toast('测试失败：' + e.message, 'error'); }
  }

  async function extractArchive() {
    if (!currentArchivePath) return;
    let outputDir;
    try { outputDir = await api('pick_output_folder'); } catch (e) { toast('选择目录失败：' + e.message, 'error'); return; }
    if (!outputDir) return;
    try {
      const res = await api('extract_archive', { path: currentArchivePath, outputDir: outputDir, password: currentPassword });
      if (res && res.ok) { toast('解压完成', 'success'); addCompleted('解压', currentArchivePath); }
      else toast('解压失败：' + ((res && res.stderr) || '未知错误'), 'error');
    } catch (e) { toast('解压失败：' + e.message, 'error'); }
  }

  // ---------- 创建压缩包 ----------

  let createPaths = [];

  function renderCreatePaths() {
    const box = $('#createPaths');
    box.innerHTML = '';
    if (!createPaths.length) {
      box.appendChild(el('span', '', '尚未选择文件'));
      return;
    }
    createPaths.forEach((p) => box.appendChild(el('span', 'path-item', p)));
  }

  function formatExt(format) {
    switch (format) {
      case '7z': return '.7z';
      case 'tar.gz': return '.tar.gz';
      case 'tar.xz': return '.tar.xz';
      default: return '.zip';
    }
  }

  // TAR 系列格式本身不加密，禁止输入密码以免产生误导。
  function updatePasswordAvailability() {
    const isTar = $('#createFormat').value.indexOf('tar') === 0;
    const pw = $('#createPassword');
    pw.disabled = isTar;
    pw.placeholder = isTar ? 'TAR 系列格式不支持密码' : '留空则不加密';
    if (isTar) pw.value = '';
  }

  async function openCreate(presetPaths) {
    createPaths = Array.isArray(presetPaths) ? presetPaths.slice() : [];
    if (!createPaths.length) {
      try { createPaths = await api('pick_sources'); } catch (e) { toast('选择失败：' + e.message, 'error'); return; }
    }
    if (!createPaths || !createPaths.length) return;
    renderCreatePaths();
    $('#createTarget').value = '';
    $('#createPassword').value = '';
    updatePasswordAvailability();
    showModal('createModal');
  }

  async function addCreatePaths() {
    let more;
    try { more = await api('pick_sources'); } catch (e) { toast('选择失败：' + e.message, 'error'); return; }
    if (more && more.length) {
      more.forEach((p) => { if (createPaths.indexOf(p) === -1) createPaths.push(p); });
      renderCreatePaths();
    }
  }

  async function pickTarget() {
    const ext = formatExt($('#createFormat').value);
    try {
      const path = await api('save_archive', { defaultName: 'archive' + ext, extension: ext });
      if (path) $('#createTarget').value = path;
    } catch (e) { toast('选择目标失败：' + e.message, 'error'); }
  }

  async function doCreate() {
    if (!createPaths.length) { toast('请先选择要压缩的文件', 'error'); return; }
    const format = $('#createFormat').value;
    const level = $('#createLevel').value;
    const password = $('#createPassword').value;
    let target = $('#createTarget').value.trim();
    try {
      if (!target) {
        const ext = formatExt(format);
        target = await api('save_archive', { defaultName: 'archive' + ext, extension: ext });
        if (!target) return;
      }
      const btn = $('#btnCreateConfirm');
      btn.disabled = true; btn.textContent = '压缩中…';
      let res;
      try {
        res = await api('create_archive', {
          paths: createPaths, target: target, format: format, level: level, password: password,
        });
      } finally {
        btn.disabled = false; btn.textContent = '开始压缩';
      }
      if (res && res.ok) {
        toast('压缩完成', 'success');
        addCompleted('创建', target);
        closeModal('createModal');
      } else {
        toast('压缩失败：' + ((res && res.stderr) || '未知错误'), 'error');
      }
    } catch (e) {
      toast('压缩失败：' + e.message, 'error');
    }
  }

  // ---------- 右键菜单 ----------

  async function refreshShell() {
    const badge = $('#shellStatusText');
    try {
      const st = await api('shell_integration_status');
      const enabled = !!(st && st.enabled);
      badge.textContent = enabled ? (st.healthy ? '已启用' : '已注册（状态异常）') : '未启用';
      badge.className = 'badge ' + (enabled ? (st.healthy ? 'ok' : 'warn') : '');
    } catch (e) {
      badge.textContent = '检测失败';
      badge.className = 'badge warn';
    }
  }

  async function shellAction(cmd, successMsg) {
    try {
      const st = await api(cmd);
      refreshShell();
      toast(successMsg + (st && st.healthy === false ? '（状态异常）' : ''), 'success');
    } catch (e) { toast('操作失败：' + e.message, 'error'); }
  }

  // ---------- 更新 ----------

  let latestInfo = null;
  let downloadTimer = null;

  async function loadVersion() {
    try {
      const d = await api('version');
      const v = 'v' + (d && d.version ? d.version : '?');
      $('#versionText').textContent = v;
      $('#updateCurrentVersion').textContent = v;
    } catch (_) { /* 忽略：版本不影响主功能 */ }
  }

  function renderUpdateInfo(info) {
    const box = $('#updateInfoBox');
    box.innerHTML = '';
    if (info && info.available) {
      box.appendChild(el('b', '', '发现新版本 ' + (info.latest_version || '可用')));
      if (info.asset_name) box.appendChild(el('div', '', '安装包：' + info.asset_name + (info.asset_size ? '（' + fmtSize(info.asset_size) + '）' : '')));
      if (info.message) box.appendChild(el('div', '', info.message));
      if (info.release_notes) {
        const notes = el('div', '', info.release_notes);
        notes.style.whiteSpace = 'pre-wrap';
        notes.style.marginTop = '8px';
        box.appendChild(notes);
      }
    } else {
      box.textContent = (info && info.message) ? info.message : '当前已是最新版本。';
    }
  }

  async function checkUpdates(silent) {
    try {
      const info = await api('check_updates');
      latestInfo = info;
      renderUpdateInfo(info);
      if (!silent) toast(info && info.available ? '发现新版本，可下载并校验' : '当前已是最新版本');
      return info;
    } catch (e) {
      if (!silent) toast('检查更新失败：' + e.message, 'error');
      throw e;
    }
  }

  function renderDownload(st) {
    const status = (st && st.status) || '';
    const box = $('#downloadBox');
    box.hidden = status === '' || status === 'idle';
    let pct = 0;
    if (st && st.total_bytes) pct = Math.min(100, Math.round((st.downloaded_bytes || 0) / st.total_bytes * 100));
    $('#progressBar').style.width = pct + '%';
    $('#downloadPercent').textContent = pct + '%';
    $('#downloadStatusText').textContent = (st && st.message) ? st.message : status;
  }

  async function pollDownload() {
    if (downloadTimer) { clearTimeout(downloadTimer); downloadTimer = null; }
    try {
      const st = await api('update_download_status');
      renderDownload(st);
      const status = (st && st.status) || '';
      if (['downloading', 'verifying', 'pending', 'started', 'checking', 'running', 'in_progress'].indexOf(status) !== -1) {
        downloadTimer = setTimeout(pollDownload, 700);
      } else if (['ready', 'done', 'completed', 'success', 'finished', 'verified'].indexOf(status) !== -1) {
        if (S.get('autoInstall', false)) installUpdate();
      }
    } catch (e) { /* 轮询失败不打断界面，下次点击可重试 */ }
  }

  async function downloadUpdate() {
    if (latestInfo && !latestInfo.can_download) { toast('当前没有可下载的更新包', 'error'); return; }
    try {
      await api('download_update');
      $('#downloadBox').hidden = false;
      pollDownload();
    } catch (e) { toast('下载失败：' + e.message, 'error'); }
  }

  async function cancelDownload() {
    try { await api('cancel_update_download'); toast('已取消下载'); pollDownload(); }
    catch (e) { toast('取消失败：' + e.message, 'error'); }
  }

  async function installUpdate() {
    const ok = await confirmDialog('安装更新', '安装更新需要退出当前程序并完成安装，是否继续？');
    if (!ok) return;
    try { await api('install_update'); toast('已启动安装程序'); }
    catch (e) { toast('安装失败：' + e.message, 'error'); }
  }

  async function pickLocalUpdate() {
    let path;
    try { path = await api('pick_update_package'); } catch (e) { toast('选择失败：' + e.message, 'error'); return; }
    if (!path) return;
    const ok = await confirmDialog('本地更新', '确定使用安装包「' + basename(path) + '」进行更新吗？');
    if (!ok) return;
    try { await api('install_local_update', { path: path }); toast('已启动安装程序'); }
    catch (e) { toast('安装失败：' + e.message, 'error'); }
  }

  async function autoUpdateFlow() {
    try {
      const info = await checkUpdates(true);
      if (info && info.available && info.can_download !== false) await downloadUpdate();
    } catch (_) { /* 自动流程静默失败 */ }
  }

  // ---------- 启动动作 ----------

  async function handleLaunchRequest() {
    try {
      const req = await api('get_launch_request');
      if (!req || !req.action) return;
      if (req.action === 'compress-zip' || req.action === 'compress-zip-here') {
        openCreate(req.path ? [req.path] : []);
      } else if (req.action === 'open-archive' && req.path) {
        openArchive(req.path);
      }
    } catch (_) { /* 无启动动作时忽略 */ }
  }

  // ---------- 绑定 ----------

  function bindUI() {
    $$('.nav-item').forEach((b) => b.addEventListener('click', () => switchView(b.dataset.view)));
    $$('[data-close]').forEach((b) => b.addEventListener('click', () => closeModal(b.dataset.close)));

    $('#btnOpenArchive').onclick = () => openArchive();
    $('#cardOpenBtn').onclick = () => openArchive();
    $('#btnCreate').onclick = () => openCreate();
    $('#cardCreateBtn').onclick = () => openCreate();

    const dz = $('#dropzone');
    ['dragenter', 'dragover'].forEach((ev) => dz.addEventListener(ev, (e) => {
      e.preventDefault(); dz.classList.add('dragover');
    }));
    ['dragleave', 'drop'].forEach((ev) => dz.addEventListener(ev, (e) => {
      e.preventDefault(); dz.classList.remove('dragover');
    }));
    dz.addEventListener('drop', (e) => {
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) {
        toast('浏览器无法读取拖放文件的本地路径，请点击「选择压缩包」。');
      }
    });
    dz.addEventListener('click', () => openArchive());
    dz.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openArchive(); }
    });

    $('#btnClearRecent').onclick = () => {
      if (!loadList('recent').length) return;
      confirmDialog('清空记录', '确定清空所有最近记录吗？').then((ok) => {
        if (ok) { saveList('recent', []); renderRecent(); }
      });
    };
    $('#btnClearCompleted').onclick = () => {
      if (!loadList('completed').length) return;
      confirmDialog('清空记录', '确定清空所有已完成记录吗？').then((ok) => {
        if (ok) { saveList('completed', []); renderCompleted(); }
      });
    };

    $('#btnTestArchive').onclick = testArchive;
    $('#btnExtractArchive').onclick = extractArchive;

    $('#btnAddCreatePaths').onclick = addCreatePaths;
    $('#btnPickTarget').onclick = pickTarget;
    $('#btnCreateConfirm').onclick = doCreate;
    $('#createFormat').onchange = updatePasswordAvailability;
    updatePasswordAvailability();

    $('#btnRegisterShell').onclick = () => shellAction('register_shell_integration', '已注册 / 修复右键菜单');
    $('#btnRemoveShell').onclick = () => shellAction('unregister_shell_integration', '已移除右键菜单');

    $('#btnCheckUpdate').onclick = () => checkUpdates(false);
    $('#btnDownloadUpdate').onclick = downloadUpdate;
    $('#btnInstallUpdate').onclick = installUpdate;
    $('#btnPickLocalUpdate').onclick = pickLocalUpdate;
    $('#btnCancelDownload').onclick = cancelDownload;

    const autoUpdate = $('#autoUpdateToggle');
    autoUpdate.checked = S.get('autoUpdate', true);
    autoUpdate.onchange = () => {
      S.set('autoUpdate', autoUpdate.checked);
      api('set_auto_check', { enabled: autoUpdate.checked }).catch(() => {});
    };
    const autoInstall = $('#autoInstallToggle');
    autoInstall.checked = S.get('autoInstall', false);
    autoInstall.onchange = () => S.set('autoInstall', autoInstall.checked);
  }

  async function init() {
    bindUI();
    switchView('files');
    renderRecent();
    renderCompleted();
    loadVersion();
    refreshShell();
    // 后端若提供自动检查开关状态则以其为准（localStorage 作为回退）。
    api('auto_check_enabled').then((d) => {
      if (d && typeof d.enabled === 'boolean') {
        $('#autoUpdateToggle').checked = d.enabled;
        S.set('autoUpdate', d.enabled);
        if (d.enabled) autoUpdateFlow();
      }
    }).catch(() => { if (S.get('autoUpdate', true)) autoUpdateFlow(); });
    handleLaunchRequest();
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
