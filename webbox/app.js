// 表格工具箱 前端逻辑：表格填充 / 多表合一 / 拆分表格
'use strict';

const $ = (id) => document.getElementById(id);

// ---------------------------------------------------------------------------
// 视图切换与公共件
// ---------------------------------------------------------------------------

document.querySelectorAll('.card.available').forEach((card) => {
  card.addEventListener('click', () => showView(card.dataset.tool));
});
$('brand-home').addEventListener('click', showHome);
document.querySelectorAll('.back-home').forEach((a) => a.addEventListener('click', showHome));

function showView(name) {
  ['home', 'fill', 'merge', 'split', 'inmerge'].forEach((v) => $('view-' + v).classList.add('hidden'));
  $('view-' + name).classList.remove('hidden');
  clearErr();
}
function showHome() { showView('home'); }

// 浏览器回退模式显示退出按钮（内嵌窗口直接关窗口即可）
if (document.body.dataset.appMode === 'browser') {
  $('btn-exit').classList.remove('hidden');
}
$('btn-exit').addEventListener('click', () => { location.href = '/api/shutdown'; });

// 错误提示：定位到当前可见视图里的 .err 元素
function curErrEl() {
  for (const v of document.querySelectorAll('main.container')) {
    if (!v.classList.contains('hidden')) {
      const e = v.querySelector('.err');
      if (e) return e;
    }
  }
  return $('err');
}
function showErr(msg) {
  const el = curErrEl();
  el.textContent = msg;
  el.classList.remove('hidden');
  el.scrollIntoView({ behavior: 'smooth', block: 'center' });
}
function clearErr() {
  document.querySelectorAll('main .err').forEach((e) => e.classList.add('hidden'));
}

async function api(url, opts) {
  const res = await fetch(url, opts);
  let data = null;
  try { data = await res.json(); } catch (e) { /* 非 JSON */ }
  if (!res.ok) {
    throw new Error((data && data.error) || ('请求失败（HTTP ' + res.status + '）'));
  }
  return data;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// 数据源下拉选项文案（隐藏表带标注）
function sheetOptText(s) {
  return (s.hidden ? '[隐藏] ' : '') + s.label + '（数据 ' + s.dataRows + ' 行）';
}

function fillSheetSelect(sel, sheets) {
  sel.innerHTML = '';
  sheets.forEach((s) => {
    const o = document.createElement('option');
    o.value = s.key;
    o.textContent = sheetOptText(s);
    sel.appendChild(o);
  });
}

// ===========================================================================
// 工具一：表格填充（V1.0 已有）
// ===========================================================================

const S = {
  src: null,  // {path, name, key, headerRow, headers, rowCount}
  tpl: null,  // 同上
  mapping: [],
  busy: false,
};

wireSide('src');
wireSide('tpl');

function wireSide(side) {
  $('file-' + side).addEventListener('change', (e) => uploadFile(side, e.target.files[0]));
  $('sel-' + side).addEventListener('change', () => {
    const st = S[side];
    if (!st) return;
    st.key = $('sel-' + side).value;
    const opt = st.sheets.find((s) => s.key === st.key);
    st.headerRow = opt ? opt.headerRow : 1;
    $('hdr-' + side).value = st.headerRow;
    readTable(side);
  });
  $('hdr-' + side).addEventListener('change', () => {
    const st = S[side];
    if (!st) return;
    st.headerRow = parseInt($('hdr-' + side).value, 10) || 1;
    readTable(side);
  });
}

async function uploadFile(side, file) {
  if (!file) return;
  clearErr();
  const fd = new FormData();
  fd.append('file', file);
  fd.append('kind', side === 'src' ? 'src' : 'tpl');
  try {
    const data = await api('/api/upload', { method: 'POST', body: fd });
    S[side] = { path: data.path, name: data.name, sheets: data.sheets, key: data.sheets[0] ? data.sheets[0].key : '', headerRow: data.sheets[0] ? data.sheets[0].headerRow : 1 };
    $('fname-' + side).textContent = data.name;
    fillSheetSelect($('sel-' + side), data.sheets);
    $('detail-' + side).classList.remove('hidden');
    $('hdr-' + side).value = S[side].headerRow;
    await readTable(side);
  } catch (err) {
    showErr(err.message);
  }
}

async function readTable(side) {
  const st = S[side];
  if (!st || !st.key) return;
  clearErr();
  try {
    const data = await api('/api/read', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: st.path, key: st.key, headerRow: st.headerRow }),
    });
    st.headers = data.headers;
    st.rowCount = data.rowCount;
    renderSide(side, data);
    renderMapping();
  } catch (err) {
    showErr(err.message);
  }
}

function renderSide(side, data) {
  $('meta-' + side).textContent = '共 ' + data.rowCount + ' 行数据，' + data.headers.filter((h) => h).length + ' 个字段';
  const chips = $('chips-' + side);
  chips.innerHTML = '';
  data.headers.forEach((h) => {
    if (!h) return;
    const c = document.createElement('span');
    c.className = 'chip';
    c.textContent = h;
    chips.appendChild(c);
  });
  if (side === 'src') renderPreview(data.preview);
}

function renderPreview(rows) {
  const t = $('prev-src');
  t.innerHTML = '';
  const head = S.src ? S.src.headers : [];
  if (head.length) {
    const tr = document.createElement('tr');
    head.forEach((h) => {
      const th = document.createElement('th');
      th.textContent = h;
      tr.appendChild(th);
    });
    t.appendChild(tr);
  }
  rows.forEach((r) => {
    const tr = document.createElement('tr');
    head.forEach((_, i) => {
      const td = document.createElement('td');
      td.textContent = r[i] || '';
      td.title = r[i] || '';
      tr.appendChild(td);
    });
    t.appendChild(tr);
  });
}

function renderMapping() {
  const list = $('map-list');
  const hint = $('map-hint');
  if (!S.tpl || !S.src || !S.tpl.headers || !S.src.headers) {
    list.innerHTML = '';
    hint.classList.remove('hidden');
    S.mapping = [];
    return;
  }
  hint.classList.add('hidden');

  S.mapping = S.tpl.headers.map((th, i) => {
    const name = (th || '').trim();
    if (!name) return -1;
    const j = S.src.headers.findIndex((sh) => (sh || '').trim() === name);
    return j;
  });

  list.innerHTML = '';
  S.tpl.headers.forEach((th, i) => {
    const row = document.createElement('div');
    row.className = 'map-row';
    const auto = S.mapping[i] >= 0;
    if (auto) row.classList.add('auto');

    const left = document.createElement('div');
    left.className = 'tpl-name';
    left.innerHTML = escapeHtml(th || ('（第' + (i + 1) + '列）')) + (auto ? '<span class="auto-tag">自动</span>' : '');

    const arrow = document.createElement('div');
    arrow.className = 'arrow';
    arrow.textContent = '→';

    const right = document.createElement('select');
    right.dataset.col = i;
    const optNone = document.createElement('option');
    optNone.value = '-1';
    optNone.textContent = '不填充';
    right.appendChild(optNone);
    S.src.headers.forEach((sh, j) => {
      if (!(sh || '').trim()) return;
      const o = document.createElement('option');
      o.value = String(j);
      o.textContent = sh;
      right.appendChild(o);
    });
    right.value = String(S.mapping[i]);
    right.addEventListener('change', () => {
      S.mapping[i] = parseInt(right.value, 10);
      row.classList.toggle('auto', false);
      const tag = row.querySelector('.auto-tag');
      if (tag) tag.remove();
    });

    row.appendChild(left);
    row.appendChild(arrow);
    row.appendChild(right);
    list.appendChild(row);
  });
}

$('btn-fill').addEventListener('click', async () => {
  clearErr();
  $('fill-result').classList.add('hidden');
  if (!S.src || !S.tpl) { showErr('请先完成第 ① 步：上传原始表格与模板表格'); return; }
  const mapped = S.mapping.filter((m) => m >= 0).length;
  if (mapped === 0) { showErr('至少要给一个模板栏目选择原始字段'); return; }

  const btn = $('btn-fill');
  btn.disabled = true;
  btn.textContent = '正在生成…';
  try {
    const data = await api('/api/fill', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tplPath: S.tpl.path, tplKey: S.tpl.key, tplHeaderRow: S.tpl.headerRow,
        srcPath: S.src.path, srcKey: S.src.key, srcHeaderRow: S.src.headerRow,
        mapping: S.mapping,
      }),
    });
    const box = $('fill-result');
    box.innerHTML = '<div class="ok-line">✅ 生成完成：共填充 ' + data.rows + ' 行数据</div>'
      + '<a href="/api/download?name=' + encodeURIComponent(data.file) + '" download>下载 ' + escapeHtml(data.file) + '</a>';
    box.classList.remove('hidden');
  } catch (err) {
    showErr(err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = '开始填充生成';
  }
});

// ===========================================================================
// 工具二：多表合一
// ===========================================================================

const M = {
  files: [],   // {path, name, sheets, key, headerRow, headers, rowCount, maps:[int], isBase}
  base: [],    // 总表字段（基准表字段＋新增）
  basePath: '',
};

$('file-merge').addEventListener('change', async (e) => {
  clearErr();
  const list = Array.from(e.target.files || []);
  e.target.value = '';
  for (const f of list) {
    try { await uploadMergeFile(f); } catch (err) { showErr(err.message); break; }
  }
});

async function uploadMergeFile(file) {
  const fd = new FormData();
  fd.append('file', file);
  fd.append('kind', 'src');
  const data = await api('/api/upload', { method: 'POST', body: fd });
  const item = {
    path: data.path, name: data.name, sheets: data.sheets,
    key: data.sheets[0] ? data.sheets[0].key : '',
    headerRow: data.sheets[0] ? data.sheets[0].headerRow : 1,
    headers: null, rowCount: 0, maps: [],
  };
  M.files.push(item);
  const idx = M.files.length - 1;
  await readMergeTable(idx);
  if (idx === 0) setMergeBase(0);
  renderMergeFiles();
}

// readMergeTable 读取某来源表数据，并按总表字段重配映射
async function readMergeTable(idx) {
  const it = M.files[idx];
  if (!it.key) return;
  const data = await api('/api/read', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: it.path, key: it.key, headerRow: it.headerRow }),
  });
  it.headers = data.headers;
  it.rowCount = data.rowCount;
  remap(it);
  renderMergeFiles();
  renderMergeMaps();
}

// remap 按总表字段对该来源表重新自动配对（同名对应，否则 -1）
function remap(it) {
  remapTo(it, M.base);
}

// remapTo 按 base 字段清单对来源表 it 重新自动配对（多表合一/表格内合并共用）
function remapTo(it, base) {
  it.maps = base.map((b) => {
    const j = (it.headers || []).findIndex((h) => (h || '').trim() === (b || '').trim());
    return j;
  });
}

// setMergeBase 以第 i 个来源表为基准：总表字段 = 其字段，所有表重配
function setMergeBase(i) {
  const it = M.files[i];
  if (!it || !it.headers) return;
  const seen = new Set();
  M.base = it.headers.filter((h) => {
    const k = (h || '').trim();
    if (!k || seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  M.basePath = it.path;
  M.files.forEach(remap);
  renderMergeFiles();
  renderMergeMaps();
}

function renderMergeFiles() {
  const box = $('merge-files');
  box.innerHTML = '';
  M.files.forEach((it, i) => {
    const card = document.createElement('div');
    card.className = 'merge-file' + (it.path === M.basePath ? ' is-base' : '');

    const title = document.createElement('div');
    title.className = 'merge-file-title';
    title.innerHTML = '<b>' + escapeHtml(it.name) + '</b>'
      + (it.path === M.basePath ? '<span class="auto-tag">基准表</span>' : '')
      + '<span class="muted">（' + (it.rowCount || '?') + ' 行数据）</span>';

    const row = document.createElement('div');
    row.className = 'field-row';

    const lab = document.createElement('label');
    lab.textContent = '数据源：';
    row.appendChild(lab);

    const sel = document.createElement('select');
    fillSheetSelect(sel, it.sheets);
    sel.value = it.key;
    sel.addEventListener('change', async () => {
      it.key = sel.value;
      const opt = it.sheets.find((s) => s.key === it.key);
      it.headerRow = opt ? opt.headerRow : 1;
      try { await readMergeTable(i); } catch (err) { showErr(err.message); }
    });
    row.appendChild(sel);

    const lab2 = document.createElement('label');
    lab2.textContent = '表头行：';
    lab2.style.marginLeft = '12px';
    row.appendChild(lab2);

    const hdr = document.createElement('input');
    hdr.type = 'number'; hdr.min = '1'; hdr.value = String(it.headerRow); hdr.className = 'num';
    hdr.addEventListener('change', async () => {
      it.headerRow = parseInt(hdr.value, 10) || 1;
      try { await readMergeTable(i); } catch (err) { showErr(err.message); }
    });
    row.appendChild(hdr);

    const btnBase = document.createElement('button');
    btnBase.className = 'btn-ghost';
    btnBase.textContent = '设为基准';
    btnBase.disabled = it.path === M.basePath;
    btnBase.addEventListener('click', () => setMergeBase(i));
    row.appendChild(btnBase);

    const btnDel = document.createElement('button');
    btnDel.className = 'btn-ghost danger';
    btnDel.textContent = '移除';
    btnDel.addEventListener('click', () => {
      M.files.splice(i, 1);
      if (it.path === M.basePath) {
        M.base = [];
        M.basePath = '';
        const first = M.files[0];
        if (first && first.headers) setMergeBase(0);
      }
      renderMergeFiles();
      renderMergeMaps();
    });
    row.appendChild(btnDel);

    card.appendChild(title);
    card.appendChild(row);
    box.appendChild(card);
  });
}

function renderMergeMaps() {
  const box = $('merge-maps');
  const hint = $('merge-map-hint');
  const chips = $('merge-base-chips');
  const dedupSel = $('merge-dedup-field');
  chips.innerHTML = '';
  dedupSel.innerHTML = '';
  if (!M.files.length || !M.base.length) {
    box.innerHTML = '';
    hint.classList.remove('hidden');
    return;
  }
  hint.classList.add('hidden');

  M.base.forEach((b) => {
    const c = document.createElement('span');
    c.className = 'chip';
    c.textContent = b;
    chips.appendChild(c);

    const o = document.createElement('option');
    o.value = String(chips.children.length - 1);
    o.textContent = b;
    dedupSel.appendChild(o);
  });

  box.innerHTML = '';
  M.files.forEach((it, i) => {
    const panel = document.createElement('div');
    panel.className = 'merge-map-panel';
    const title = document.createElement('div');
    title.className = 'merge-map-title';
    const sheetName = it.key.startsWith('xlsx:') ? it.key.slice(5) : it.key;
    title.innerHTML = '<b>' + escapeHtml(it.name) + '</b> · ' + escapeHtml(sheetName) + ' → 总表字段';
    panel.appendChild(title);

    M.base.forEach((b, c) => {
      const row = document.createElement('div');
      row.className = 'map-row' + (it.maps[c] >= 0 ? ' auto' : '');

      const left = document.createElement('div');
      left.className = 'tpl-name';
      left.textContent = b;
      if (it.maps[c] >= 0) {
        const tag = document.createElement('span');
        tag.className = 'auto-tag';
        tag.textContent = '自动';
        left.appendChild(tag);
      }

      const arrow = document.createElement('div');
      arrow.className = 'arrow';
      arrow.textContent = '→';

      const right = document.createElement('select');
      const optNone = document.createElement('option');
      optNone.value = '-1';
      optNone.textContent = '留空';
      right.appendChild(optNone);
      (it.headers || []).forEach((h, j) => {
        if (!(h || '').trim()) return;
        const o = document.createElement('option');
        o.value = String(j);
        o.textContent = h;
        right.appendChild(o);
      });
      right.value = String(it.maps[c] === undefined ? -1 : it.maps[c]);
      right.addEventListener('change', () => {
        it.maps[c] = parseInt(right.value, 10);
        row.classList.toggle('auto', it.maps[c] >= 0);
        const tag = row.querySelector('.auto-tag');
        if (it.maps[c] >= 0 && !tag) {
          const t = document.createElement('span');
          t.className = 'auto-tag';
          t.textContent = '自动';
          left.appendChild(t);
        } else if (it.maps[c] < 0 && tag) {
          tag.remove();
        }
      });

      row.appendChild(left);
      row.appendChild(arrow);
      row.appendChild(right);
      panel.appendChild(row);
    });
    box.appendChild(panel);
  });
}

$('btn-merge-addfield').addEventListener('click', () => {
  const inp = $('merge-new-field');
  const name = (inp.value || '').trim();
  if (!name) { showErr('请输入要新增的字段名'); return; }
  if (M.base.includes(name)) { showErr('总表已有字段「' + name + '」'); return; }
  clearErr();
  M.base.push(name);
  M.files.forEach((it) => {
    it.maps.push((it.headers || []).findIndex((h) => (h || '').trim() === name));
  });
  inp.value = '';
  renderMergeMaps();
});

$('btn-merge').addEventListener('click', async () => {
  clearErr();
  $('merge-result').classList.add('hidden');
  if (!M.files.length) { showErr('请先上传要合并的表格'); return; }
  if (!M.base.length) { showErr('总表没有字段，请设置基准表'); return; }

  let dedup = -1;
  if ($('merge-dedup-on').checked) {
    dedup = parseInt($('merge-dedup-field').value, 10);
    if (isNaN(dedup) || dedup < 0) { showErr('请选择判重字段'); return; }
  }

  const btn = $('btn-merge');
  btn.disabled = true;
  btn.textContent = '正在合并…';
  try {
    const data = await api('/api/merge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        base: M.base,
        sources: M.files.map((it) => ({ path: it.path, key: it.key, headerRow: it.headerRow, mapping: it.maps })),
        sourceCol: $('merge-source-col').checked,
        dedup: dedup,
        basePath: M.basePath || M.files[0].path,
      }),
    });
    const box = $('merge-result');
    box.innerHTML = '<div class="ok-line">✅ 合并完成：' + M.files.length + ' 个来源，共 ' + data.rows + ' 行数据</div>'
      + '<a href="/api/download?name=' + encodeURIComponent(data.file) + '" download>下载 ' + escapeHtml(data.file) + '</a>';
    box.classList.remove('hidden');
  } catch (err) {
    showErr(err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = '开始合并';
  }
});

// ===========================================================================
// 工具三：拆分表格
// ===========================================================================

const P = { st: null, headers: [], rowCount: 0 };

$('file-split').addEventListener('change', async (e) => {
  const f = e.target.files[0];
  e.target.value = '';
  if (!f) return;
  clearErr();
  try {
    const fd = new FormData();
    fd.append('file', f);
    fd.append('kind', 'src');
    const data = await api('/api/upload', { method: 'POST', body: fd });
    P.st = { path: data.path, name: data.name, sheets: data.sheets, key: data.sheets[0] ? data.sheets[0].key : '', headerRow: data.sheets[0] ? data.sheets[0].headerRow : 1 };
    $('fname-split').textContent = data.name;
    fillSheetSelect($('sel-split'), data.sheets);
    $('detail-split').classList.remove('hidden');
    $('hdr-split').value = P.st.headerRow;
    $('btn-split').disabled = true;
    await readSplitTable();
  } catch (err) {
    showErr(err.message);
  }
});

$('sel-split').addEventListener('change', async () => {
  if (!P.st) return;
  P.st.key = $('sel-split').value;
  const opt = P.st.sheets.find((s) => s.key === P.st.key);
  P.st.headerRow = opt ? opt.headerRow : 1;
  $('hdr-split').value = P.st.headerRow;
  await readSplitTable();
});
$('hdr-split').addEventListener('change', async () => {
  if (!P.st) return;
  P.st.headerRow = parseInt($('hdr-split').value, 10) || 1;
  await readSplitTable();
});

async function readSplitTable() {
  if (!P.st || !P.st.key) return;
  try {
    const data = await api('/api/read', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: P.st.path, key: P.st.key, headerRow: P.st.headerRow }),
    });
    P.headers = data.headers;
    P.rowCount = data.rowCount;
    $('meta-split').textContent = '共 ' + data.rowCount + ' 行数据，' + data.headers.filter((h) => h).length + ' 个字段';
    const sel = $('split-field');
    sel.innerHTML = '';
    data.headers.forEach((h, i) => {
      if (!(h || '').trim()) return;
      const o = document.createElement('option');
      o.value = String(i);
      o.textContent = h;
      sel.appendChild(o);
    });
    $('btn-split').disabled = data.rowCount === 0;
    renderSplitPreview(data.preview);
  } catch (err) {
    showErr(err.message);
  }
}

function renderSplitPreview(rows) {
  const t = $('prev-split');
  t.innerHTML = '';
  const head = P.headers;
  if (head.length) {
    const tr = document.createElement('tr');
    head.forEach((h) => {
      const th = document.createElement('th');
      th.textContent = h;
      tr.appendChild(th);
    });
    t.appendChild(tr);
  }
  (rows || []).forEach((r) => {
    const tr = document.createElement('tr');
    head.forEach((_, i) => {
      const td = document.createElement('td');
      td.textContent = r[i] || '';
      td.title = r[i] || '';
      tr.appendChild(td);
    });
    t.appendChild(tr);
  });
}

$('btn-split').addEventListener('click', async () => {
  clearErr();
  $('split-result').classList.add('hidden');
  if (!P.st) { showErr('请先上传总表'); return; }
  const mode = document.querySelector('input[name="split-mode"]:checked').value;
  let field = 0, size = 0;
  if (mode === 'byValue') {
    field = parseInt($('split-field').value, 10);
    if (isNaN(field) || field < 0) { showErr('请选择拆分字段'); return; }
  } else {
    size = parseInt($('split-size').value, 10) || 0;
    if (size < 1) { showErr('每份行数必须 ≥ 1'); return; }
  }

  const btn = $('btn-split');
  btn.disabled = true;
  btn.textContent = '正在拆分…';
  try {
    const data = await api('/api/split', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        path: P.st.path, key: P.st.key, headerRow: P.st.headerRow,
        mode: mode, field: field, size: size, outFmt: $('split-outfmt').value,
      }),
    });
    const box = $('split-result');
    let html = '<div class="ok-line">✅ 拆分完成：共 ' + data.files.length + ' 份</div>';
    data.files.forEach((f) => {
      html += '<div><a href="/api/download?name=' + encodeURIComponent(f.file) + '" download>' + escapeHtml(f.file) + '</a> <span class="muted">（' + f.rows + ' 行）</span></div>';
    });
    html += '<div style="margin-top:8px"><a id="btn-zip" class="btn-ghost" href="#">📦 打包下载全部（zip）</a></div>';
    box.innerHTML = html;
    const zipBtn = $('btn-zip');
    const names = data.files.map((f) => f.file).join(',');
    zipBtn.href = '/api/zip?names=' + encodeURIComponent(names);
    zipBtn.setAttribute('download', '');
    box.classList.remove('hidden');
  } catch (err) {
    showErr(err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = '开始拆分';
  }
});

// ===========================================================================
// 工具四：表格内合并（V1.2：同一文件内的多个工作表/表格 → 一张总表）
// ===========================================================================

const IM = {
  path: '', name: '',
  sheets: [],  // 服务端返回的 SheetInfo 清单（含 hidden 标注）
  items: {},   // key -> {key,label,hidden,checked,headerRow,headers,rowCount,maps}
  baseKey: '', // 基准表 key
  base: [],    // 总表字段（基准表字段＋新增）
};

$('file-inmerge').addEventListener('change', async (e) => {
  const f = e.target.files[0];
  e.target.value = '';
  if (!f) return;
  clearErr();
  try {
    const fd = new FormData();
    fd.append('file', f);
    fd.append('kind', 'src');
    const data = await api('/api/upload', { method: 'POST', body: fd });
    IM.path = data.path;
    IM.name = data.name;
    IM.sheets = data.sheets;
    IM.items = {};
    IM.baseKey = '';
    IM.base = [];
    $('fname-inmerge').textContent = data.name;
    $('detail-inmerge').classList.remove('hidden');
    $('inmerge-result').classList.add('hidden');
    renderInSheets();
    renderInMaps();
    // 默认勾选第一张有数据的表
    const first = data.sheets.find((s) => s.dataRows > 0);
    if (first) await toggleInSheet(IM.items[first.key], true);
  } catch (err) {
    showErr(err.message);
  }
});

function inCheckedItems() {
  return IM.sheets.map((s) => IM.items[s.key]).filter((x) => x.checked && x.headers);
}

// renderInSheets 渲染工作表勾选清单
function renderInSheets() {
  const box = $('inmerge-sheets');
  box.innerHTML = '';
  IM.sheets.forEach((s) => {
    const it = IM.items[s.key] || (IM.items[s.key] = { key: s.key, label: s.label, hidden: s.hidden, checked: false, headerRow: s.headerRow || 1, headers: null, rowCount: s.dataRows, maps: [] });
    const row = document.createElement('div');
    row.className = 'inmerge-sheet' + (it.checked ? ' checked' : '') + (it.key === IM.baseKey ? ' is-base' : '');

    const ck = document.createElement('label');
    ck.className = 'ck inmerge-ck';
    const box1 = document.createElement('input');
    box1.type = 'checkbox';
    box1.checked = it.checked;
    box1.addEventListener('change', () => toggleInSheet(it, box1.checked));
    ck.appendChild(box1);
    const txt = document.createElement('span');
    txt.textContent = sheetOptText(s);
    ck.appendChild(txt);
    row.appendChild(ck);

    if (it.checked) {
      const lab2 = document.createElement('label');
      lab2.textContent = '表头行：';
      lab2.style.marginLeft = '10px';
      row.appendChild(lab2);
      const hdr = document.createElement('input');
      hdr.type = 'number'; hdr.min = '1'; hdr.value = String(it.headerRow); hdr.className = 'num';
      hdr.addEventListener('change', async () => {
        it.headerRow = parseInt(hdr.value, 10) || 1;
        try {
          await readInSheet(it);
          setInBase(IM.baseKey);
          renderInSheets(); renderInMaps();
        } catch (err) { showErr(err.message); }
      });
      row.appendChild(hdr);

      const btnBase = document.createElement('button');
      btnBase.className = 'btn-ghost';
      btnBase.textContent = '设为基准';
      btnBase.disabled = it.key === IM.baseKey;
      btnBase.addEventListener('click', () => { setInBase(it.key); renderInSheets(); renderInMaps(); });
      row.appendChild(btnBase);

      if (it.key === IM.baseKey) {
        const tag = document.createElement('span');
        tag.className = 'auto-tag';
        tag.textContent = '基准表';
        row.appendChild(tag);
      }
    }
    box.appendChild(row);
  });
}

// toggleInSheet 勾选/取消勾选一个工作表（勾选时读取其数据）
async function toggleInSheet(it, on) {
  it.checked = on;
  if (on && !it.headers) {
    try {
      await readInSheet(it);
    } catch (err) {
      it.checked = false;
      showErr(err.message);
      renderInSheets();
      return;
    }
  }
  if (!on && IM.baseKey === it.key) {
    IM.baseKey = '';
    const first = inCheckedItems()[0];
    if (first) IM.baseKey = first.key;
  }
  if (on && !IM.baseKey) IM.baseKey = it.key;
  if (IM.baseKey) setInBase(IM.baseKey);
  else IM.base = [];
  renderInSheets();
  renderInMaps();
}

// readInSheet 读取某工作表数据（按其表头行）
async function readInSheet(it) {
  const data = await api('/api/read', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: IM.path, key: it.key, headerRow: it.headerRow }),
  });
  it.headers = data.headers;
  it.rowCount = data.rowCount;
  remapTo(it, IM.base);
}

// setInBase 以指定工作表为基准：总表字段 = 其字段（去重空列），全部来源重配
function setInBase(key) {
  const it = IM.items[key];
  if (!it || !it.headers) return;
  const seen = new Set();
  IM.base = it.headers.filter((h) => {
    const k = (h || '').trim();
    if (!k || seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  IM.baseKey = key;
  IM.sheets.forEach((s) => remapTo(IM.items[s.key], IM.base));
}

$('btn-inmerge-addfield').addEventListener('click', () => {
  const inp = $('inmerge-new-field');
  const name = (inp.value || '').trim();
  if (!name) { showErr('请输入要新增的字段名'); return; }
  if (IM.base.includes(name)) { showErr('总表已有字段「' + name + '」'); return; }
  clearErr();
  IM.base.push(name);
  IM.sheets.forEach((s) => {
    const it = IM.items[s.key];
    it.maps.push((it.headers || []).findIndex((h) => (h || '').trim() === name));
  });
  inp.value = '';
  renderInMaps();
});

// renderInMaps 渲染总表字段 chips 与各勾选来源的映射面板
function renderInMaps() {
  const box = $('inmerge-maps');
  const hint = $('inmerge-map-hint');
  const chips = $('inmerge-base-chips');
  const dedupSel = $('inmerge-dedup-field');
  chips.innerHTML = '';
  dedupSel.innerHTML = '';
  const srcs = inCheckedItems();
  if (!srcs.length || !IM.base.length) {
    box.innerHTML = '';
    hint.classList.remove('hidden');
    return;
  }
  hint.classList.add('hidden');

  IM.base.forEach((b, c) => {
    const el = document.createElement('span');
    el.className = 'chip';
    el.textContent = b;
    chips.appendChild(el);

    const o = document.createElement('option');
    o.value = String(c);
    o.textContent = b;
    dedupSel.appendChild(o);
  });

  box.innerHTML = '';
  srcs.forEach((it) => {
    const panel = document.createElement('div');
    panel.className = 'merge-map-panel';
    const title = document.createElement('div');
    title.className = 'merge-map-title';
    const sheetName = it.key.startsWith('xlsx:') ? it.key.slice(5) : it.key.replace(/^docx:/, '表格');
    title.innerHTML = '<b>' + escapeHtml(it.label || sheetName) + '</b>'
      + (it.key === IM.baseKey ? '<span class="auto-tag">基准表</span>' : '')
      + ' → 总表字段';
    panel.appendChild(title);

    IM.base.forEach((b, c) => {
      const row = document.createElement('div');
      row.className = 'map-row' + ((it.maps[c] || -1) >= 0 ? ' auto' : '');

      const left = document.createElement('div');
      left.className = 'tpl-name';
      left.textContent = b;
      if ((it.maps[c] || -1) >= 0) {
        const tag = document.createElement('span');
        tag.className = 'auto-tag';
        tag.textContent = '自动';
        left.appendChild(tag);
      }

      const arrow = document.createElement('div');
      arrow.className = 'arrow';
      arrow.textContent = '→';

      const right = document.createElement('select');
      const optNone = document.createElement('option');
      optNone.value = '-1';
      optNone.textContent = '留空';
      right.appendChild(optNone);
      (it.headers || []).forEach((h, j) => {
        if (!(h || '').trim()) return;
        const o = document.createElement('option');
        o.value = String(j);
        o.textContent = h;
        right.appendChild(o);
      });
      right.value = String(it.maps[c] === undefined ? -1 : it.maps[c]);
      right.addEventListener('change', () => {
        it.maps[c] = parseInt(right.value, 10);
        row.classList.toggle('auto', it.maps[c] >= 0);
        const tag = row.querySelector('.auto-tag');
        if (it.maps[c] >= 0 && !tag) {
          const t = document.createElement('span');
          t.className = 'auto-tag';
          t.textContent = '自动';
          left.appendChild(t);
        } else if (it.maps[c] < 0 && tag) {
          tag.remove();
        }
      });

      row.appendChild(left);
      row.appendChild(arrow);
      row.appendChild(right);
      panel.appendChild(row);
    });
    box.appendChild(panel);
  });
}

$('btn-inmerge').addEventListener('click', async () => {
  clearErr();
  $('inmerge-result').classList.add('hidden');
  const srcs = inCheckedItems();
  if (!srcs.length) { showErr('请先勾选要合并的工作表'); return; }
  if (!IM.base.length) { showErr('总表没有字段，请设置基准表'); return; }

  let dedup = -1;
  if ($('inmerge-dedup-on').checked) {
    dedup = parseInt($('inmerge-dedup-field').value, 10);
    if (isNaN(dedup) || dedup < 0) { showErr('请选择判重字段'); return; }
  }

  const btn = $('btn-inmerge');
  btn.disabled = true;
  btn.textContent = '正在合并…';
  try {
    const data = await api('/api/merge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        base: IM.base,
        sources: srcs.map((it) => ({ path: IM.path, key: it.key, headerRow: it.headerRow, mapping: it.maps, name: it.label })),
        sourceCol: $('inmerge-source-col').checked,
        dedup: dedup,
        basePath: IM.path,
        outFmt: $('inmerge-outfmt').value,
      }),
    });
    const box = $('inmerge-result');
    box.innerHTML = '<div class="ok-line">✅ 合并完成：' + srcs.length + ' 个工作表，共 ' + data.rows + ' 行数据</div>'
      + '<a href="/api/download?name=' + encodeURIComponent(data.file) + '" download>下载 ' + escapeHtml(data.file) + '</a>';
    box.classList.remove('hidden');
  } catch (err) {
    showErr(err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = '开始合并';
  }
});
