/* 通用签到表生成器 V2.0 - 八步向导逻辑 + 多语种支持 */
"use strict";

/* ---------------------------------------------------------------- i18n 多语种 */
const LANGS = [
  ["zh-CN", "中文"], ["en", "English"], ["fr", "Français"], ["de", "Deutsch"],
  ["ru", "Русский"], ["ar", "العربية"], ["ja", "日本語"], ["ko", "한국어"],
];
const RTL_LANGS = ["ar"];
let I18N_ZH = {};   // 中文基准（兜底）
let LANG_DATA = {}; // 当前语言
let CUR_LANG = localStorage.getItem("appLang") || "zh-CN";

function fmtStr(str, params) {
  return String(str).replace(/\{(\w+)\}/g, (m, k) => (params && params[k] != null) ? params[k] : m);
}
function t(key, params) {
  let s = (LANG_DATA && LANG_DATA[key] != null) ? LANG_DATA[key]
        : (I18N_ZH[key] != null ? I18N_ZH[key] : key);
  return params ? fmtStr(s, params) : s;
}
function applyI18n() {
  document.querySelectorAll("[data-i18n]").forEach(el => { el.textContent = t(el.dataset.i18n); });
  document.querySelectorAll("[data-i18n-title]").forEach(el => { el.title = t(el.dataset.i18nTitle); });
  document.querySelectorAll("[data-i18n-ph]").forEach(el => { el.placeholder = t(el.dataset.i18nPh); });
  document.documentElement.lang = CUR_LANG;
  document.documentElement.dir = RTL_LANGS.includes(CUR_LANG) ? "rtl" : "ltr";
  const sel = $("lang-select");
  if (sel) sel.value = CUR_LANG;
}
async function initI18n() {
  try {
    I18N_ZH = await (await fetch("locales/zh-CN.json")).json();
  } catch (e) { I18N_ZH = {}; }
  try {
    if (CUR_LANG !== "zh-CN") {
      LANG_DATA = await (await fetch("locales/" + CUR_LANG + ".json")).json();
    } else {
      LANG_DATA = {}; // 🔴 切回中文必须清空上一语言缓存，否则 t() 仍优先读到旧语言
    }
  } catch (e) { LANG_DATA = {}; CUR_LANG = "zh-CN"; }
  applyI18n();
  // 动态文案刷新：下一步按钮文本依赖 noRoster 状态，由 JS 设置而非 data-i18n
  if ($("input-next")) refreshInputNext();
  gotoStep(S.current);
}
function switchLang(lang) {
  CUR_LANG = lang;
  localStorage.setItem("appLang", lang);
  initI18n().then(() => {
    // 重渲染已显示的动态区域（静态 data-i18n 文案已由 applyI18n 刷新）
    loadTemplateLists();
    if (S.roster) renderSheets();
    if (S.columns.length && S.current === "fields") renderRoles();
    if (S.tpl && S.current === "mapping") renderMapping();
    if (S.current === "fill") renderFillForm();
    if (S.outputs.length && S.current === "output") renderOutput();
    if (S.outputs.length && S.current === "catout") renderCatout();
  });
}

const STEP_LABEL = { input: "step.input", dedup: "step.dedup", fields: "step.fields", mapping: "step.mapping", fill: "step.fill", preview: "step.preview", output: "step.output", catout: "step.catout" };

const S = {
  current: "input",
  noRoster: false,
  roster: null,      // {kind, sheets}
  chosen: [],        // [{key, headerRow}]
  columns: [],       // per-sheet {key,label,headers,count}
  roles: {},         // key -> {nameCol, fillCols, catCol, excludeCol, excludeVal}
  merges: [],        // [[from,into]]
  tpl: null,
  fields: [],
  tplSource: "",     // builtin|upload|library
  remainTotal: null,
  hasCat: false,
  outputs: [],
  fillFields: [],    // 第③步勾选自动填入的字段名（全表合并去重）
};

// ---------------------------------------------------------------- 工具
function $(id) { return document.getElementById(id); }
function toast(msg, ms) {
  const el = $("toast");
  el.textContent = msg; el.hidden = false;
  clearTimeout(el._timer);
  el._timer = setTimeout(() => { el.hidden = true; }, ms || 2600);
}
// 后端错误消息翻译：{error:原文, code:err.xxx, detail:剥离前缀后的动态部分}
function errText(data) {
  if (data && data.code && (LANG_DATA[data.code] != null || I18N_ZH[data.code] != null)) {
    return t(data.code, { msg: data.detail || "", ext: data.detail || "", f: data.detail || "" });
  }
  return (data && data.error) || "HTTP Error";
}
async function api(path, opts) {
  let resp;
  try {
    resp = await fetch(path, opts);
  } catch (e) {
    // fetch 网络层失败＝本机服务不在了（程序被关闭或未启动）
    throw new Error(t("err.conn"));
  }
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(errText(data) || ("HTTP " + resp.status));
  return data;
}
// 面板内醒目错误条：不自动消失、就在操作按钮上方，解决"点了没反应"感知问题
function showErr(msg) {
  clearErr();
  const panel = $("step-" + S.current);
  if (!panel) { toast(msg, 8000); return; }
  const bar = document.createElement("div");
  bar.id = "inline-err";
  bar.style.cssText = "background:#fdecea;color:#b3261e;border:1px solid #f5c6c0;border-radius:8px;padding:10px 14px;margin:10px 0;font-size:13px;";
  bar.textContent = "⚠ " + msg;
  panel.insertBefore(bar, panel.querySelector(".actions") || panel.firstChild.nextSibling);
}
function clearErr() {
  document.querySelectorAll("#inline-err").forEach(e => e.remove());
}
function post(path, body) {
  return api(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}
function stepList() {
  const s = ["input"];
  if (!S.noRoster) s.push("dedup", "fields", "mapping");
  s.push("fill", "preview", "output");
  if (S.hasCat) s.push("catout");
  return s;
}
function gotoStep(name) {
  S.current = name;
  clearErr();
  const list = stepList();
  document.querySelectorAll(".panel").forEach(p => { p.hidden = true; });
  $("step-" + name).hidden = false;
  const idx = list.indexOf(name);
  $("stepper").innerHTML = list.map((s, i) => {
    let cls = "stp";
    if (s === name) cls += " on"; else if (i < idx) cls += " done";
    return `<span class="${cls}">${esc(t(STEP_LABEL[s]))}</span>`;
  }).join("");
  window.scrollTo(0, 0);
}
function backToPrev() {
  gotoStep(S.noRoster ? "input" : "mapping");
}
function refreshInputNext() {
  const ready = !!S.tpl && (S.noRoster || S.chosen.length > 0);
  $("input-next").disabled = !ready;
  $("input-next").textContent = S.noRoster ? t("btn.nextDedupShort") : t("btn.nextDedup");
}

// ---------------------------------------------------------------- 第1步 输入
$("opt-upload").onclick = () => $("roster-file").click();
$("roster-file").onchange = async (e) => {
  const f = e.target.files[0];
  if (!f) return;
  const fd = new FormData();
  fd.append("file", f);
  try {
    const data = await api("/api/upload/roster", { method: "POST", body: fd });
    S.roster = data; S.noRoster = false;
    // 只自动勾选第一张有数据的表：真实花名册常带大量历史杂表（本工(2)、离职人员、福利统计…），
    // 全勾会把无关表拖进后续判重和字段设置，用户还必须逐表配置；需要哪几张由用户显式勾选
    const firstData = data.sheets.find(s => s.dataRows > 0);
    S.chosen = firstData ? [{ key: firstData.key, headerRow: firstData.headerRow }] : [];
    renderSheets();
    refreshInputNext();
    toast(t("toast.rosterOk", { n: data.sheets.length, name: firstData ? firstData.label : "" }));
  } catch (err) { toast(t("toast.readFail", { msg: err.message }), 4000); }
  e.target.value = "";
};
$("opt-noroster").onclick = () => {
  const yes = confirm(t("confirm.noRoster"));
  if (!yes) return;
  S.noRoster = true;
  post("/api/mode/noroster", { value: true }).then(refreshInputNext);
};
function renderSheets() {
  const box = $("roster-sheets");
  box.innerHTML = `<h3>${esc(t("s1.sheetsTitle"))}</h3>`;
  S.roster.sheets.forEach((sh, i) => {
    const checked = S.chosen.some(c => c.key === sh.key);
    const div = document.createElement("div");
    div.className = "sheet-block";
    div.innerHTML = `
      <label class="sh-title"><input type="checkbox" data-i="${i}" ${checked ? "checked" : ""}> ${esc(sh.label)}</label>
      <div class="form-row"><label>${esc(t("s1.headerRow"))}</label><input type="text" class="hr-input" data-i="${i}" value="${sh.headerRow}" ${checked ? "" : "disabled"}></div>
      <div class="form-row"><label>${esc(t("s1.summary"))}</label><span class="hint">${fmtStr(t("s1.rowsData"), { n: sh.dataRows, h: esc((sh.headers || []).slice(0, 8).join(" | ")) })}</span></div>`;
    box.appendChild(div);
  });
  box.querySelectorAll("input[type=checkbox]").forEach(cb => {
    cb.onchange = () => {
      const i = +cb.dataset.i, sh = S.roster.sheets[i];
      const block = cb.closest(".sheet-block");
      block.querySelector(".hr-input").disabled = !cb.checked;
      if (cb.checked) S.chosen.push({ key: sh.key, headerRow: +block.querySelector(".hr-input").value || sh.headerRow });
      else S.chosen = S.chosen.filter(c => c.key !== sh.key);
      refreshInputNext();
    };
  });
  box.querySelectorAll(".hr-input").forEach(inp => {
    inp.onchange = () => {
      const c = S.chosen.find(c => c.key === S.roster.sheets[+inp.dataset.i].key);
      if (c) c.headerRow = +inp.value || c.headerRow;
    };
  });
}
$("input-next").onclick = async () => {
  if (!S.tpl) { showErr(t("err.needTpl")); return; }
  if (S.noRoster) {
    try {
      await post("/api/mode/noroster", { value: true });
      renderFillForm();
      gotoStep("fill");
    } catch (err) { toast(err.message, 4000); }
    return;
  }
  if (S.chosen.length === 0) { showErr(t("err.needSheet")); return; }
  try {
    const data = await post("/api/roster/config", { sheets: S.chosen });
    S.columns = data.columns; S.roles = data.roles;
    renderRoles();
    gotoStep("dedup");
    loadDupGroups();
  } catch (err) { toast(err.message, 4000); }
};

// 模板三个页签
document.querySelectorAll(".tab").forEach(el => {
  el.onclick = () => {
    document.querySelectorAll(".tab").forEach(x => x.classList.remove("active"));
    el.classList.add("active");
    ["builtin", "upload", "library"].forEach(k => { $("tab-" + k).hidden = k !== el.dataset.tab; });
    if (el.dataset.tab === "library") loadTemplateLists();
  };
});
// 词条兜底取值：带前缀查翻译，查不到（返回原 key）时回退显示原始数据名
function tb(prefix, raw) {
  const s = t(prefix + raw);
  return s === prefix + raw ? raw : s;
}
// 内置模板文件名 → 当前语言显示名（会议-现代灰 → Meeting - Modern Gray）
// 类型/风格任一没查到词条时整体回退原文件名，避免"Meeting-现代灰"混搭
function tplDisplayName(filename) {
  const name = String(filename).replace(/\.docx$/, "");
  const m = name.match(/^(.+?)-(.+)$/);
  if (!m) return name;
  const t1 = tb("btplType.", m[1]);
  const t2 = tb("btplStyle.", m[2]);
  return (t1 === m[1] && t2 === m[2]) ? name : t1 + " - " + t2;
}
async function loadTemplateLists() {
  try {
    const data = await api("/api/template/library");
    const grid = $("builtin-list");
    grid.innerHTML = (data.builtins || []).map(b => {
      const name = b.replace(/\.docx$/, "");
      const m = name.match(/^(.+?)-(.+)$/);
      const t1 = m ? tb("btplType.", m[1]) : name;
      const t2 = m ? tb("btplStyle.", m[2]) : "";
      return `<div class="tpl-card" data-id="${esc(b)}"><div class="t1">${esc(t1)}</div><div class="t2">${esc(t2)}${esc(t("s1.word"))}</div><button class="small tpl-pv" data-id="${esc(b)}" data-name="${esc(name)}">${esc(t("btn.preview"))}</button></div>`;
    }).join("");
    grid.querySelectorAll(".tpl-card").forEach(card => {
      card.onclick = () => selectBuiltin(card.dataset.id, card);
    });
    grid.querySelectorAll(".tpl-pv").forEach(btn => {
      btn.onclick = (e) => { e.stopPropagation(); openTplPreview(btn.dataset.id, btn.dataset.name, btn); };
    });
    const lib = $("library-list");
    lib.innerHTML = (data.library || []).length === 0
      ? `<p class='hint'>${esc(t("lib.empty"))}</p>`
      : "<table class='tbl'><tr><th>" + esc(t("th.tplName")) + "</th><th>" + esc(t("th.format")) + "</th><th>" + esc(t("th.savedAt")) + "</th><th></th></tr>" +
        data.library.map(e => `<tr><td>${esc(e.name)}</td><td>${e.kind}</td><td>${esc(e.savedAt)}</td><td><button class="small" data-name="${esc(e.name)}" data-kind="${e.kind}">${esc(t("btn.use"))}</button></td></tr>`).join("") +
        "</table>";
    lib.querySelectorAll("button[data-name]").forEach(btn => {
      btn.onclick = () => loadLibrary(btn.dataset.name, btn.dataset.kind);
    });
  } catch (err) { toast(err.message, 4000); }
}
async function selectBuiltin(id, card) {
  if (card) {
    document.querySelectorAll(".tpl-card").forEach(c => c.classList.remove("sel"));
    card.classList.add("sel");
  }
  try {
    const data = await post("/api/template/builtin", { id });
    onTemplateReady(data, "builtin");
  } catch (err) { toast(err.message, 4000); }
}

// 模板预览弹窗：内置模板按 id 预览；已选模板（上传/库调用后）不传 id 直接预览当前
let pvPendingId = null, pvPendingCard = null;
async function openTplPreview(id, name, card) {
  const body = $("tpl-preview-body");
  pvPendingId = id; pvPendingCard = card || null;
  $("tpl-preview-title").textContent = t("tpl.previewName", { name: name ? tplDisplayName(name) : t("tpl.curName") });
  $("tpl-preview-use").hidden = !id;
  body.innerHTML = `<p class='hint'>${esc(t("tpl.loading"))}</p>`;
  $("tpl-preview-modal").hidden = false;
  try {
    const url = id ? "/api/template/preview?id=" + encodeURIComponent(id) : "/api/template/preview";
    const resp = await fetch(url);
    if (!resp.ok) throw new Error((await resp.json()).error || t("s6.pvFail"));
    body.innerHTML = await resp.text();
  } catch (err) {
    const msg = String(err.message || err);
    body.innerHTML = `<p class='hint'>${esc(t("tpl.fail", { msg: msg === "Failed to fetch" ? t("tpl.failConn") : esc(msg) }))}</p>`;
  }
}
$("tpl-preview-close").onclick = () => { $("tpl-preview-modal").hidden = true; };
$("tpl-preview-use").onclick = () => {
  $("tpl-preview-modal").hidden = true;
  if (pvPendingId) selectBuiltin(pvPendingId, pvPendingCard);
};
async function loadLibrary(name, kind) {
  try {
    const data = await post("/api/template/library-load", { name, kind });
    onTemplateReady(data, "library");
    toast(t("toast.libLoaded", { name }));
  } catch (err) { toast(err.message, 4000); }
}
$("tpl-upload-card").onclick = () => $("tpl-file").click();
$("tpl-file").onchange = async (e) => {
  const f = e.target.files[0];
  if (!f) return;
  const fd = new FormData();
  fd.append("file", f);
  try {
    const data = await api("/api/template/upload", { method: "POST", body: fd });
    onTemplateReady(data, "upload");
    $("tpl-save-area").hidden = false;
    toast(t("toast.tplOk"));
  } catch (err) { toast(err.message, 4500); }
  e.target.value = "";
};
function onTemplateReady(data, source) {
  S.tpl = data.analysis; S.fields = data.fields; S.tplSource = source;
  const a = data.analysis;
  const blankN = (a.tplFields || []).filter(f => f.kind === "blank").length;
  const colN = (a.dataCols || []).filter(c => !c.isName).length;
  $("tpl-analysis").innerHTML = `
    <div class="sheet-block" style="margin-top:14px;">
      <div class="sh-title">${esc(t("ana.title"))}</div>
      <div class="form-row"><label>${esc(t("ana.label"))}</label><span>${esc(a.title || t("ana.noTitle"))}</span></div>
      <div class="form-row"><label>${esc(t("ana.format"))}</label><span>${a.kind === "xlsx" ? "Excel" : "Word"}${a.sheet ? esc(t("ana.sheet", { s: a.sheet })) : ""}</span></div>
      <div class="form-row"><label>${esc(t("ana.cap"))}</label><span><b>${a.capacity}</b> ${esc(t("ana.person"))}（${a.dataRows} × ${a.nameCols.length}）</span></div>
      <div class="form-row"><label>${esc(t("ana.recog"))}</label><span>${esc(t("ana.recogVal", { cols: a.dataCols.length, names: a.nameCols.length, others: colN, fields: (a.tplFields || []).length, blank: blankN }))}</span></div>
      <div class="form-row"><label></label><span><button class="small" id="tpl-cur-pv">${esc(t("ana.pv"))}</button></span></div>
    </div>`;
  const curPv = $("tpl-cur-pv");
  if (curPv) curPv.onclick = () => openTplPreview("", t("tpl.curName"), null);
  $("tpl-save-area").hidden = source !== "upload";
  $("tpl-save-check").checked = false;
  $("tpl-save-name").hidden = true;
  refreshInputNext();
}
$("tpl-save-check").onchange = (e) => { $("tpl-save-name").hidden = !e.target.checked; };
$("tpl-name").onchange = saveTpl;
function saveTpl() {
  const name = $("tpl-name").value.trim();
  if (!name) return;
  post("/api/template/save", { name }).then(() => toast(t("toast.savedLib", { name }))).catch(err => toast(err.message, 4000));
}

// ---------------------------------------------------------------- 第2步 判重
async function loadDupGroups() {
  const box = $("dup-groups");
  box.innerHTML = `<p class='hint'>${esc(t("s2.analyzing"))}</p>`;
  try {
    const data = await post("/api/dedup/prepare", {});
    if (!data.groups || data.groups.length === 0) {
      box.innerHTML = `<p class='hint'>${esc(t("s2.none"))}</p>`;
      return;
    }
    box.innerHTML = "";
    data.groups.forEach((g, gi) => {
      const div = document.createElement("div");
      div.className = "dup-group";
      const keyPreview = g.items[0].cells.filter(c => c).slice(0, 4).join(" / ");
      div.innerHTML = `<div class="dg-title">${esc(t("s2.group", { n: gi + 1, c: g.items.length, k: keyPreview }))}</div>`;
      g.items.forEach((it, ii) => {
        const row = document.createElement("div");
        row.className = "dup-item";
        row.dataset.g = gi; row.dataset.i = ii; row.dataset.remove = it.remove;
        row.innerHTML = `
          <span class="cells">[${esc(it.sheetLabel)}] ${esc(it.cells.filter(c => c).join(" | "))}</span>
          <button class="${it.remove ? "" : "on-keep"}" data-v="keep">${esc(t("btn.keep"))}</button>
          <button class="${it.remove ? "on-del" : ""}" data-v="del">${esc(t("btn.dup"))}</button>`;
        div.appendChild(row);
      });
      box.appendChild(div);
    });
    box.querySelectorAll(".dup-item button").forEach(btn => {
      btn.onclick = () => {
        const item = btn.closest(".dup-item");
        const del = btn.dataset.v === "del";
        item.dataset.remove = del;
        const [bKeep, bDel] = item.querySelectorAll("button");
        bKeep.className = del ? "" : "on-keep";
        bDel.className = del ? "on-keep" : "on-del";
      };
    });
    box._groups = data.groups;
  } catch (err) { box.innerHTML = ""; toast(err.message, 4000); }
}
$("dedup-next").onclick = async () => {
  const box = $("dup-groups");
  const groups = box._groups || [];
  const remove = [];
  box.querySelectorAll(".dup-item").forEach(item => {
    if (item.dataset.remove === "true") {
      const g = groups[+item.dataset.g];
      if (g) { const it = g.items[+item.dataset.i]; remove.push({ sheet: it.sheet, row: it.row }); }
    }
  });
  try {
    const data = await post("/api/dedup/apply", { remove });
    S.remainTotal = data.total;
    const removedN = remove.length;
    toast(removedN ? t("toast.marked", { n: removedN, t: data.total }) : t("toast.noDup", { t: data.total }));
    renderRoles();
    gotoStep("fields");
  } catch (err) { toast(err.message, 4000); }
};

// ---------------------------------------------------------------- 第3步 名单字段用途
const PURPOSE = [["fill", "purpose.fill"], ["cat", "purpose.cat"], ["exclude", "purpose.exclude"], ["none", "purpose.none"]];
function purposeOf(role, col) {
  if ((role.fillCols || []).includes(col)) return "fill";
  if (role.catCol === col) return "cat";
  if (role.excludeCol === col) return "exclude";
  return "none";
}
function renderRoles() {
  const box = $("field-maps");
  box.innerHTML = "";
  // 只渲染勾选参与生成的工作表（S.chosen），无关的历史表不出现
  const chosenKeys = new Set(S.chosen.map(c => c.key));
  S.columns.filter(c => chosenKeys.has(c.key)).forEach((c) => {
    const role = S.roles[c.key] || { nameCol: -1, fillCols: [], catCol: -1, excludeCol: -1, excludeVal: "" };
    const rows = c.headers.map((h, i) => {
      const p = purposeOf(role, i);
      return `<tr>
        <td>${esc(h || t("s3.emptyCol"))} <small class="hint">${esc(t("s3.colN", { n: i + 1 }))}</small></td>
        <td><select data-key="${esc(c.key)}" data-col="${i}">${PURPOSE.map(([v, k]) =>
          `<option value="${v}" ${p === v ? "selected" : ""}>${esc(t(k))}</option>`).join("")}</select></td>
        <td><input type="text" class="excl-val" data-key="${esc(c.key)}" data-col="${i}" value="${esc(role.excludeCol === i ? (role.excludeVal || "") : "")}" placeholder="${esc(t("s3.exclPh"))}" style="width:100%;${p === "exclude" ? "" : "visibility:hidden"}"></td>
      </tr>`;
    }).join("");
    const div = document.createElement("div");
    div.className = "sheet-block";
    div.innerHTML = `
      <div class="sh-title">${esc(c.label)}　<small class="hint">${esc(t("s3.rowsData", { n: c.count }))}</small></div>
      <table class="tbl"><tr><th>${esc(t("th.rosterCol"))}</th><th>${esc(t("th.purpose"))}</th><th>${esc(t("th.exclVal"))}</th></tr>${rows}</table>`;
    box.appendChild(div);
  });
  box.querySelectorAll("select[data-key]").forEach(sel => {
    sel.onchange = () => {
      const tr = sel.closest("tr");
      const inp = tr.querySelector(".excl-val");
      inp.style.visibility = sel.value === "exclude" ? "visible" : "hidden";
    };
  });
  renderMerges();
}
function renderMerges() {
  const box = $("merge-rules");
  box.innerHTML = "";
  S.merges.forEach((m, i) => {
    const row = document.createElement("div");
    row.className = "fill-dynamic";
    row.innerHTML = `
      <input type="text" class="mg-from" value="${esc(m[0])}" placeholder="${esc(t("s3.mergeFromPh"))}" style="max-width:220px;">
      <span>${esc(t("s3.mergeArrow"))}</span>
      <input type="text" class="mg-into" value="${esc(m[1])}" placeholder="${esc(t("s3.mergeIntoPh"))}" style="max-width:220px;">
      <button class="small" onclick="this.parentElement.remove()">${esc(t("btn.del"))}</button>`;
    box.appendChild(row);
  });
}
function addMergeRule() {
  S.merges.push(["", ""]);
  renderMerges();
}
$("fields-next").onclick = async () => {
  const roles = {};
  document.querySelectorAll("select[data-key][data-col]").forEach(sel => {
    const k = sel.dataset.key, col = +sel.dataset.col, p = sel.value;
    roles[k] = roles[k] || { nameCol: -1, fillCols: [], catCol: -1, excludeCol: -1, excludeVal: "" };
    if (p === "fill") roles[k].fillCols.push(col);
    else if (p === "cat") roles[k].catCol = col;
    else if (p === "exclude") roles[k].excludeCol = col;
  });
  document.querySelectorAll("input.excl-val").forEach(inp => {
    const k = inp.dataset.key, col = +inp.dataset.col;
    if (roles[k] && roles[k].excludeCol === col) roles[k].excludeVal = inp.value.trim();
  });
  // 姓名列：优先保留原姓名列（若仍为自动填入），否则取第一个自动填入列
  for (const k of Object.keys(roles)) {
    const old = S.roles[k] || {};
    if (roles[k].fillCols.includes(old.nameCol)) roles[k].nameCol = old.nameCol;
    else if (roles[k].fillCols.length > 0) roles[k].nameCol = roles[k].fillCols[0];
  }
  const merges = [];
  document.querySelectorAll("#merge-rules .fill-dynamic").forEach(row => {
    const f = row.querySelector(".mg-from").value.trim();
    const to = row.querySelector(".mg-into").value.trim();
    if (f && to) merges.push([f, to]);
  });
  // 校验（只针对勾选参与的工作表；错误具体到表名并醒目显示）
  const chosenKeys = new Set(S.chosen.map(c => c.key));
  const bad = Object.keys(roles).filter(k => chosenKeys.has(k) && roles[k].fillCols.length === 0);
  if (bad.length) {
    const label = bad.map(k => {
      const c = S.columns.find(c => c.key === k);
      return c ? "「" + c.label + "」" : k;
    }).join("、");
    showErr(t("err.needFill", { labels: label }));
    return;
  }
  S.merges = merges;
  try {
    await post("/api/roster/roles", { roles, merges });
    S.roles = Object.assign({}, S.roles, roles);
    computeFillFields();
    renderMapping();
    gotoStep("mapping");
  } catch (err) { toast(err.message, 4000); }
};
function computeFillFields() {
  const set = [];
  S.columns.forEach(c => {
    const role = S.roles[c.key] || {};
    (role.fillCols || []).forEach(col => {
      const name = c.headers[col] || t("s3.colN", { n: col + 1 });
      if (!set.includes(name)) set.push(name);
    });
  });
  S.fillFields = set;
}

// ---------------------------------------------------------------- 第4步 对应关系
const CONTROL_LABEL = { text: "ctrl.text", longtext: "ctrl.longtext", list: "ctrl.list", persons: "ctrl.persons", select: "ctrl.select", location: "ctrl.location", date: "ctrl.date", time: "ctrl.time", auto: "ctrl.auto" };
const SOURCE_LABEL = [["fill", "src.fill"], ["auto", "src.auto"], ["roster", "src.roster"], ["skip", "src.skip"]];
function fieldOptions(sel, withBlank, blankText) {
  let html = withBlank ? `<option value="">${esc(blankText || t("opt.hand"))}</option>` : "";
  html += S.fillFields.map(f => `<option value="${esc(f)}" ${f === sel ? "selected" : ""}>${esc(f)}</option>`).join("");
  return html;
}
function renderMapping() {
  // 上：数据区列映射
  const mbox = $("mapping-list");
  mbox.innerHTML = "";
  const dc = S.tpl && S.tpl.dataCols ? S.tpl.dataCols : [];
  if (!dc.length) {
    mbox.innerHTML = `<p class='hint'>${esc(t("s4.noCols"))}</p>`;
  }
  // 姓名列不参与映射（姓名由生成器自动依次填入，映射会导致同一人名复制到多个姓名格）
  S._mapping = (S._mapping || []).filter(m => !dc.find(c => c.colIdx === m.colIdx && c.isName));
  if (!S._mapping.length) S._mapping = defaultMapping(dc);
  dc.forEach(col => {
    const div = document.createElement("div");
    div.className = "form-row";
    if (col.isName) {
      div.innerHTML = `
        <label>${esc(col.label)} <b>${esc(t("s4.nameCol"))}</b> <small class="hint">${esc(t("s4.col", { n: col.colIdx }))}</small></label>
        <span class="hint">${esc(t("s4.autoFill"))}</span>`;
      mbox.appendChild(div);
      return;
    }
    const cur = (S._mapping.find(m => m.colIdx === col.colIdx) || {}).field || "";
    div.innerHTML = `
      <label>${esc(col.label)} <small class="hint">${esc(t("s4.col", { n: col.colIdx }))}</small></label>
      <select data-col="${col.colIdx}">${fieldOptions(cur, true)}</select>`;
    mbox.appendChild(div);
  });
  mbox.querySelectorAll("select[data-col]").forEach(sel => {
    sel.onchange = () => {
      const ci = +sel.dataset.col;
      const m = S._mapping.find(m => m.colIdx === ci);
      if (m) m.field = sel.value; else S._mapping.push({ colIdx: ci, field: sel.value });
    };
  });
  renderDesign();
}
function defaultMapping(dc) {
  const out = [];
  dc.forEach(col => {
    if (col.isName) return; // 姓名列自动填入，绝不映射（防同一人名重复多格）
    let f = S.fillFields.find(x => x === col.label) ||
        S.fillFields.find(x => x.includes(col.label) || col.label.includes(x)) || "";
    if (f) out.push({ colIdx: col.colIdx, field: f });
  });
  return out;
}
function renderDesign() {
  const box = $("design-list");
  if (!S.fields.length) {
    box.innerHTML = `<p class='hint'>${t("s4.noFields")}</p>`;
    return;
  }
  box.innerHTML = "";
  S.fields.forEach((f, i) => {
    const row = document.createElement("div");
    row.className = "design-row v2";
    row.innerHTML = `
      <span class="dn"><input type="text" class="fname" data-i="${i}" value="${esc(f.name)}" style="width:100%;">
        <small>${f.kind === "blank" ? esc(t("field.blank")) : esc(t("field.ph"))}${f.default ? esc(t("field.prefill", { v: f.default.slice(0, 12) })) : ""}</small></span>
      <select data-i="${i}" data-k="mode">
        ${SOURCE_LABEL.map(([v, k]) => `<option value="${v}" ${f.mode === v ? "selected" : ""}>${esc(t(k))}</option>`).join("")}
      </select>
      <select data-i="${i}" data-k="rosterField" ${f.mode === "roster" ? "" : "hidden"}>${fieldOptions(f.rosterField, false)}</select>
      <select data-i="${i}" data-k="control" ${f.mode === "fill" ? "" : "hidden"}>
        ${Object.entries(CONTROL_LABEL).map(([k, kk]) => `<option value="${k}" ${f.control === k ? "selected" : ""}>${esc(t(kk))}</option>`).join("")}
      </select>`;
    box.appendChild(row);
  });
  box.querySelectorAll(".fname").forEach(inp => {
    inp.onchange = () => { S.fields[+inp.dataset.i].name = inp.value.trim() || S.fields[+inp.dataset.i].name; };
  });
  box.querySelectorAll("select[data-k]").forEach(sel => {
    sel.onchange = () => {
      const f = S.fields[+sel.dataset.i];
      f[sel.dataset.k] = sel.value;
      if (sel.dataset.k === "mode") {
        const row = sel.closest(".design-row");
        row.querySelector("select[data-k=rosterField]").hidden = sel.value !== "roster";
        row.querySelector("select[data-k=control]").hidden = sel.value !== "fill";
      }
    };
  });
}
$("mapping-next").onclick = async () => {
  try {
    await post("/api/mapping", { mapping: (S._mapping || []).filter(m => m.field) });
    await post("/api/template/design", { design: S.fields });
    renderFillForm();
    gotoStep("fill");
  } catch (err) { toast(err.message, 4000); }
};

// ---------------------------------------------------------------- 第5步 填写
function dynInput(placeholder, max) {
  const wrap = document.createElement("div");
  const inner = document.createElement("div");
  const addBtn = document.createElement("button");
  addBtn.className = "small"; addBtn.type = "button";
  wrap.appendChild(inner); wrap.appendChild(addBtn);
  const count = () => inner.children.length;
  const updateBtn = () => {
    addBtn.textContent = max ? t("dyn.addMax", { c: count(), m: max }) : t("dyn.add");
    addBtn.disabled = max ? count() >= max : false;
  };
  const addRow = (val) => {
    const r = document.createElement("div");
    r.className = "fill-dynamic";
    r.innerHTML = `<input type="text" placeholder="${esc(placeholder)}"> <button type="button" class="small">－</button>`;
    r.querySelector("button").onclick = () => { r.remove(); updateBtn(); };
    if (val) r.querySelector("input").value = val;
    inner.appendChild(r);
    updateBtn();
  };
  addRow("");
  addBtn.onclick = () => addRow("");
  wrap._values = () => Array.from(inner.querySelectorAll("input")).map(i => i.value.trim()).filter(Boolean);
  return wrap;
}
function renderFillForm() {
  const box = $("fill-form");
  box.innerHTML = "";
  const fillable = S.fields.filter(f => f.mode === "fill");
  if (!fillable.length) box.innerHTML = `<p class='hint'>${esc(t("s5.none"))}</p>`;
  fillable.forEach(f => {
    const row = document.createElement("div");
    row.className = "form-row";
    const label = document.createElement("label");
    label.textContent = f.name;
    row.appendChild(label);
    const cell = document.createElement("div");
    cell.dataset.fname = f.name;
    let input;
    switch (f.control) {
      case "list": cell.appendChild(dynInput(t("ph.content"))); break;
      case "persons": cell.appendChild(dynInput(t("ph.name"), 5)); break;
      case "location": cell.appendChild(dynInput(t("ph.place"), 3)); break;
      case "select": {
        input = document.createElement("select");
        (f.options || []).forEach(o => { const op = document.createElement("option"); op.textContent = o; input.appendChild(op); });
        cell.appendChild(input); break;
      }
      case "date": input = document.createElement("input"); input.type = "date"; cell.appendChild(input); break;
      case "time": {
        const w = document.createElement("div");
        w.style.display = "flex"; w.style.gap = "8px"; w.style.alignItems = "center";
        const t1 = document.createElement("input"); t1.type = "time"; t1.value = "09:00";
        const t2 = document.createElement("input"); t2.type = "time"; t2.value = "11:30";
        w.appendChild(t1); w.appendChild(document.createTextNode(t("time.to"))); w.appendChild(t2);
        w._values = () => [(t1.value && t2.value ? t1.value + "-" + t2.value : (t1.value || t2.value || ""))];
        cell.appendChild(w); break;
      }
      case "longtext": input = document.createElement("textarea"); input.rows = 2; input.style.width = "100%"; cell.appendChild(input); break;
      default: input = document.createElement("input"); input.type = "text"; input.style.width = "100%"; cell.appendChild(input);
    }
    row.appendChild(cell);
    box.appendChild(row);
  });
  // 拆分容量
  if (!S.noRoster && S.tpl) {
    const row = document.createElement("div");
    row.className = "form-row";
    row.innerHTML = `<label>${esc(t("s5.capLabel"))}</label>
      <div>${esc(t("s5.capPre"))} <input type="text" id="cap-input" value="${S.tpl.capacity}" style="width:70px;"> ${esc(t("ana.person"))}
      <span class="hint">${esc(t("s5.capHint", { m: S.tpl.capacity }))}</span></div>`;
    box.appendChild(row);
  }
  // 自动/名单来源字段说明
  const autos = S.fields.filter(f => f.mode === "auto" || f.mode === "roster");
  if (autos.length) {
    const p = document.createElement("p");
    p.className = "hint";
    p.innerHTML = esc(t("s5.autoNote")) + autos.map(f =>
        esc(f.name) + (f.mode === "roster" ? esc(t("src.rosterFrom", { f: f.rosterField || t("src.rosterNone") }))
          : f.name.includes("人数") ? (S.remainTotal != null ? esc(t("s5.peopleEq", { n: S.remainTotal })) : "") : "")
      ).join("、");
    box.appendChild(p);
  }
}
$("gen-btn").onclick = async () => {
  clearErr();
  const fill = {};
  try {
    document.querySelectorAll("#fill-form [data-fname]").forEach(cell => {
      const name = cell.dataset.fname;
      const w = cell.firstChild;
      if (w && w._values) {
        const v = w._values();
        fill[name] = Array.isArray(v) ? v.join("；") : String(v || "");
        return;
      }
      const ta = cell.querySelector("textarea");
      if (ta) { fill[name] = ta.value.trim(); return; }
      const inp = cell.querySelector("input,select");
      if (inp) fill[name] = inp.value.trim();
    });
  } catch (err) { showErr(t("err.fillRead", { msg: err.message })); return; }
  const cap = +($("cap-input") ? $("cap-input").value : 0);
  const btn = $("gen-btn");
  btn.disabled = true; btn.textContent = t("btn.genBusy");
  try {
    const data = await post("/api/generate", { fill, capacity: cap });
    S.outputs = data.outputs || [];
    S.hasCat = !!data.hasCat;
    renderPreview();
    gotoStep("preview");
  } catch (err) { showErr(err.message); }
  btn.disabled = false; btn.textContent = t("btn.gen");
};

// ---------------------------------------------------------------- 第6步 预览
function renderPreview() {
  $("preview-summary").innerHTML = t("s6.summary", { n: S.outputs.length, t: S.outputs.reduce((a, o) => a + o.count, 0) });
  const list = $("preview-list");
  list.innerHTML = "";
  S.outputs.forEach((o) => {
    const block = document.createElement("div");
    block.className = "pv-block";
    block.innerHTML = `
      <div class="pv-head-bar">
        <span><b>${esc(o.file)}</b>　${o.group ? esc(t("s6.group", { g: o.group })) : ""}${esc(t("s6.part", { p: o.part, parts: o.parts, c: o.count }))}</span>
      </div>
      <div class="pv-body" data-f="${encodeURIComponent(o.file)}"><p class="hint">${esc(t("tpl.loading"))}</p></div>`;
    list.appendChild(block);
    fetch("/api/preview?file=" + encodeURIComponent(o.file))
      .then(r => r.text())
      .then(html => { block.querySelector(".pv-body").innerHTML = html; })
      .catch(() => { block.querySelector(".pv-body").innerHTML = `<p class='hint'>${esc(t("s6.pvFail"))}</p>`; });
  });
}
$("to-output").onclick = () => { renderOutput(); gotoStep("output"); };

// ---------------------------------------------------------------- 第7步 输出
function renderOutput() {
  const total = S.outputs.reduce((a, o) => a + o.count, 0);
  $("output-summary").innerHTML = t("s7.summary", {
    g: new Set(S.outputs.map(o => o.group)).size, n: S.outputs.length, t: total,
  });
  const list = $("output-list");
  list.innerHTML = S.outputs.map(o => `
    <div class="out-row">
      <span class="out-name">${esc(o.file)}</span>
      <span class="hint">${o.group ? esc(o.group) + " · " : ""}${esc(t("s6.part", { p: o.part, parts: o.parts, c: o.count }))}</span>
      <a class="button small" href="/api/download?file=${encodeURIComponent(o.file)}">${esc(t("btn.dlWord"))}</a>
    </div>`).join("");
  $("dl-all").href = "/api/download-all";
  $("dl-all2").href = "/api/download-all";
  $("to-catout").hidden = !S.hasCat;
  $("finish-btn").hidden = S.hasCat;
}
$("to-catout").onclick = () => { renderCatout(); gotoStep("catout"); };

// ---------------------------------------------------------------- 第8步 分类输出
function renderCatout() {
  const byGroup = {};
  S.outputs.forEach(o => {
    (byGroup[o.group] = byGroup[o.group] || []).push(o);
  });
  const box = $("catout-list");
  box.innerHTML = Object.keys(byGroup).map(g => {
    const items = byGroup[g];
    return `
    <div class="sheet-block">
      <div class="sh-title">${esc(g || t("s8.uncat"))}　<small class="hint">${esc(t("s8.gSummary", { n: items.length, t: items.reduce((a, o) => a + o.count, 0) }))}</small></div>
      ${items.map(o => `
        <div class="out-row">
          <span class="out-name">${esc(o.file)}</span>
          <span class="hint">${o.count} ${esc(t("ana.person"))}</span>
          <a class="button small" href="/api/download?file=${encodeURIComponent(o.file)}">${esc(t("btn.dl"))}</a>
        </div>`).join("")}
      <div class="out-row"><span></span><span></span>
        <a class="button small primary" href="/api/download-group?group=${encodeURIComponent(g)}">${esc(t("btn.dlGroup"))}</a>
      </div>
    </div>`;
  }).join("");
}

// ---------------------------------------------------------------- 重做 / 取消
$("redo-btn").onclick = () => { $("redo-modal").hidden = false; };
function closeRedo() { $("redo-modal").hidden = true; }
async function redoNew() {
  await post("/api/reset", {});
  window.location.reload();
}
function redoKeep() {
  closeRedo();
  gotoStep("input");
}
$("finish-btn").onclick = () => toast(t("toast.done"));
$("finish-btn2").onclick = () => toast(t("toast.done"));
$("cancel-btn").onclick = async () => {
  if (!confirm(t("confirm.cancel"))) return;
  await post("/api/reset", {});
  window.location.reload();
};

// 上一步/弹窗按钮绑定（onclick 内联已改为 id，便于 CSP 与统一管理）
$("back-input").onclick = () => gotoStep("input");
$("back-dedup").onclick = () => gotoStep("dedup");
$("back-fields").onclick = () => gotoStep("fields");
$("back-mapping").onclick = () => backToPrev();
$("back-fill").onclick = () => gotoStep("fill");
$("back-output").onclick = () => gotoStep("output");
$("add-merge").onclick = () => addMergeRule();
$("redo-new").onclick = () => redoNew();
$("redo-keep").onclick = () => redoKeep();
$("redo-back").onclick = () => closeRedo();

// ---------------------------------------------------------------- 退出按钮：仅浏览器回退模式显示
// WebView 内嵌模式下关窗口即退出，按钮冗余；浏览器模式（WebView2 不可用回退 / 直接访问
// http://127.0.0.1:17877）关标签页不会结束进程，需要此按钮停止服务
(function initExitBtn() {
  const mode = window.APP_MODE || "webview";
  if (mode === "webview") {
    const link = $("exit-link");
    if (link) link.style.display = "none";
  }
})();

// ---------------------------------------------------------------- 语言切换器
(function initLangSel() {
  const sel = $("lang-select");
  sel.innerHTML = LANGS.map(([code, name]) => `<option value="${code}">${name}</option>`).join("");
  sel.value = CUR_LANG;
  sel.onchange = () => switchLang(sel.value);
})();

// ---------------------------------------------------------------- 启动
gotoStep("input");
// 语言包加载完成后再渲染模板列表，避免词条键（如 btn.preview）在翻译就绪前露出
initI18n().then(() => loadTemplateLists());
