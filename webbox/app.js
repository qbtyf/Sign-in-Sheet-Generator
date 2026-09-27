// 表格工具箱 - 表格填充工具前端逻辑
'use strict';

const S = {
  src: null,  // {path, name, key, headerRow, headers, rowCount}
  tpl: null,  // 同上
  mapping: [],
  busy: false,
};

const $ = (id) => document.getElementById(id);

// ---------------------------------------------------------------------------
// 视图切换
// ---------------------------------------------------------------------------

document.querySelectorAll('.card.available').forEach((card) => {
  card.addEventListener('click', () => {
    const tool = card.dataset.tool;
    if (tool === 'fill') {
      $('view-home').classList.add('hidden');
      $('view-fill').classList.remove('hidden');
    }
  });
});
$('brand-home').addEventListener('click', showHome);
$('back-home').addEventListener('click', showHome);
function showHome() {
  $('view-fill').classList.add('hidden');
  $('view-home').classList.remove('hidden');
}

// 浏览器回退模式显示退出按钮（内嵌窗口直接关窗口即可）
if (document.body.dataset.appMode === 'browser') {
  $('btn-exit').classList.remove('hidden');
}
$('btn-exit').addEventListener('click', () => { location.href = '/api/shutdown'; });

// ---------------------------------------------------------------------------
// 错误提示
// ---------------------------------------------------------------------------

function showErr(msg) {
  const el = $('err');
  el.textContent = msg;
  el.classList.remove('hidden');
  el.scrollIntoView({ behavior: 'smooth', block: 'center' });
}
function clearErr() { $('err').classList.add('hidden'); }

async function api(url, opts) {
  const res = await fetch(url, opts);
  let data = null;
  try { data = await res.json(); } catch (e) { /* 非 JSON */ }
  if (!res.ok) {
    throw new Error((data && data.error) || ('请求失败（HTTP ' + res.status + '）'));
  }
  return data;
}

// ---------------------------------------------------------------------------
// 第①步：上传与数据源选择
// ---------------------------------------------------------------------------

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
    const sel = $('sel-' + side);
    sel.innerHTML = '';
    data.sheets.forEach((s) => {
      const o = document.createElement('option');
      o.value = s.key;
      o.textContent = s.label + '（数据 ' + s.dataRows + ' 行）';
      sel.appendChild(o);
    });
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

// ---------------------------------------------------------------------------
// 第②步：字段对齐
// ---------------------------------------------------------------------------

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

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// ---------------------------------------------------------------------------
// 第③步：生成
// ---------------------------------------------------------------------------

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
