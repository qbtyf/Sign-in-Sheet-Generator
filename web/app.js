/* 通用签到表生成器 V2 - 八步向导逻辑 */
"use strict";

const STEP_LABEL = { input: "① 输入", dedup: "② 判重", fields: "③ 名单字段", mapping: "④ 对应关系", fill: "⑤ 填写", preview: "⑥ 预览", output: "⑦ 输出", catout: "⑧ 分类输出" };

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
  const t = $("toast");
  t.textContent = msg; t.hidden = false;
  clearTimeout(t._timer);
  t._timer = setTimeout(() => { t.hidden = true; }, ms || 2600);
}
async function api(path, opts) {
  let resp;
  try {
    resp = await fetch(path, opts);
  } catch (e) {
    // fetch 网络层失败＝本机服务不在了（程序被关闭或未启动）
    throw new Error("无法连接本机服务——程序可能已退出。请重新双击「通用签到表生成器.exe」后再试。");
  }
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || ("HTTP " + resp.status));
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
    return `<span class="${cls}">${STEP_LABEL[s]}</span>`;
  }).join("");
  window.scrollTo(0, 0);
}
function backToPrev() {
  gotoStep(S.noRoster ? "input" : "mapping");
}
function refreshInputNext() {
  const ready = !!S.tpl && (S.noRoster || S.chosen.length > 0);
  $("input-next").disabled = !ready;
  $("input-next").textContent = S.noRoster ? "下一步：填写信息 →" : "下一步：判重 →";
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
    toast("名单读取成功：共 " + data.sheets.length + " 个工作表，已自动勾选「" + (firstData ? firstData.label : "") + "」。如需其他表（如协力工），请在下方勾选");
  } catch (err) { toast("读取失败：" + err.message, 4000); }
  e.target.value = "";
};
$("opt-noroster").onclick = () => {
  const yes = confirm("是否确认【不需要填写人员名单】？\n\n确定 → 仅填写信息，生成姓名栏留空的空白签到表（将跳过判重/名单字段/对应关系）；\n取消 → 请点击「上传名单」。");
  if (!yes) return;
  S.noRoster = true;
  post("/api/mode/noroster", { value: true }).then(refreshInputNext);
};
function renderSheets() {
  const box = $("roster-sheets");
  box.innerHTML = "<h3>工作表（勾选参与生成的人员表）</h3>";
  S.roster.sheets.forEach((sh, i) => {
    const checked = S.chosen.some(c => c.key === sh.key);
    const div = document.createElement("div");
    div.className = "sheet-block";
    div.innerHTML = `
      <label class="sh-title"><input type="checkbox" data-i="${i}" ${checked ? "checked" : ""}> ${esc(sh.label)}</label>
      <div class="form-row"><label>表头所在行</label><input type="text" class="hr-input" data-i="${i}" value="${sh.headerRow}" ${checked ? "" : "disabled"}></div>
      <div class="form-row"><label>概要</label><span class="hint">${sh.dataRows} 行数据 · 表头：${esc((sh.headers || []).slice(0, 8).join(" | "))}</span></div>`;
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
  if (!S.tpl) { showErr("请先在右侧选择签到表模板（内置 12 种任选其一，点卡片即选中）"); return; }
  if (S.noRoster) {
    try {
      await post("/api/mode/noroster", { value: true });
      renderFillForm();
      gotoStep("fill");
    } catch (err) { toast(err.message, 4000); }
    return;
  }
  if (S.chosen.length === 0) { showErr("请上传名单并在左侧勾选参与生成的工作表（只勾需要的那几张，如：本工、协力工）"); return; }
  try {
    const data = await post("/api/roster/config", { sheets: S.chosen });
    S.columns = data.columns; S.roles = data.roles;
    renderRoles();
    gotoStep("dedup");
    loadDupGroups();
  } catch (err) { toast(err.message, 4000); }
};

// 模板三个页签
document.querySelectorAll(".tab").forEach(t => {
  t.onclick = () => {
    document.querySelectorAll(".tab").forEach(x => x.classList.remove("active"));
    t.classList.add("active");
    ["builtin", "upload", "library"].forEach(k => { $("tab-" + k).hidden = k !== t.dataset.tab; });
    if (t.dataset.tab === "library") loadTemplateLists();
  };
});
async function loadTemplateLists() {
  try {
    const data = await api("/api/template/library");
    const grid = $("builtin-list");
    grid.innerHTML = (data.builtins || []).map(b => {
      const name = b.replace(/\.docx$/, "");
      const m = name.match(/^(.+?)-(.+)$/);
      return `<div class="tpl-card" data-id="${esc(b)}"><div class="t1">${esc(m ? m[1] : name)}</div><div class="t2">${esc(m ? m[2] : "")} · Word</div><button class="small tpl-pv" data-id="${esc(b)}" data-name="${esc(name)}">预览</button></div>`;
    }).join("");
    grid.querySelectorAll(".tpl-card").forEach(card => {
      card.onclick = () => selectBuiltin(card.dataset.id, card);
    });
    grid.querySelectorAll(".tpl-pv").forEach(btn => {
      btn.onclick = (e) => { e.stopPropagation(); openTplPreview(btn.dataset.id, btn.dataset.name, btn); };
    });
    const lib = $("library-list");
    lib.innerHTML = (data.library || []).length === 0
      ? "<p class='hint'>模板库还是空的。上传模板后勾选「保存到模板库」，下次就能直接调用。</p>"
      : "<table class='tbl'><tr><th>模板名</th><th>格式</th><th>保存时间</th><th></th></tr>" +
        data.library.map(e => `<tr><td>${esc(e.name)}</td><td>${e.kind}</td><td>${esc(e.savedAt)}</td><td><button class="small" data-name="${esc(e.name)}" data-kind="${e.kind}">调用</button></td></tr>`).join("") +
        "</table>";
    lib.querySelectorAll("button[data-name]").forEach(btn => {
      btn.onclick = () => loadLibrary(btn.dataset.name, btn.dataset.kind);
    });
  } catch (err) { toast(err.message, 4000); }
}
loadTemplateLists();
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
  $("tpl-preview-title").textContent = "模板预览：" + (name || "当前模板");
  $("tpl-preview-use").hidden = !id;
  body.innerHTML = "<p class='hint'>预览加载中……</p>";
  $("tpl-preview-modal").hidden = false;
  try {
    const url = id ? "/api/template/preview?id=" + encodeURIComponent(id) : "/api/template/preview";
    const resp = await fetch(url);
    if (!resp.ok) throw new Error((await resp.json()).error || "预览失败");
    body.innerHTML = await resp.text();
  } catch (err) {
    const msg = String(err.message || err);
    body.innerHTML = "<p class='hint'>预览加载失败：" +
      (msg === "Failed to fetch"
        ? "无法连接本机服务——程序可能已退出，请重新双击「通用签到表生成器.exe」后刷新本页面。"
        : esc(msg)) + "</p>";
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
    toast("已调用模板：" + name);
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
    toast("模板解析成功");
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
      <div class="sh-title">模板解析结果</div>
      <div class="form-row"><label>模板标题</label><span>${esc(a.title || "（未识别，将用「签到表」命名）")}</span></div>
      <div class="form-row"><label>格式</label><span>${a.kind === "xlsx" ? "Excel" : "Word"}${a.sheet ? " · 工作表：" + esc(a.sheet) : ""}</span></div>
      <div class="form-row"><label>单张容量</label><span><b>${a.capacity}</b> 人（${a.dataRows} 行 × ${a.nameCols.length} 个姓名栏）</span></div>
      <div class="form-row"><label>识别结果</label><span>数据区可映射列 ${a.dataCols.length} 个（姓名列 ${a.nameCols.length} 个${colN ? "、其他 " + colN + " 个" : ""}）· 信息字段 ${(a.tplFields || []).length} 个（留空识别 ${blankN} 个）</span></div>
      <div class="form-row"><label></label><span><button class="small" id="tpl-cur-pv">预览此模板</button></span></div>
    </div>`;
  const curPv = $("tpl-cur-pv");
  if (curPv) curPv.onclick = () => openTplPreview("", "当前模板", null);
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
  post("/api/template/save", { name }).then(() => toast("已保存到模板库：" + name)).catch(err => toast(err.message, 4000));
}

// ---------------------------------------------------------------- 第2步 判重
async function loadDupGroups() {
  const box = $("dup-groups");
  box.innerHTML = "<p class='hint'>正在分析重复项……</p>";
  try {
    const data = await post("/api/dedup/prepare", {});
    if (!data.groups || data.groups.length === 0) {
      box.innerHTML = "<p class='hint'>未发现重复条目，可直接进入下一步。</p>";
      return;
    }
    box.innerHTML = "";
    data.groups.forEach((g, gi) => {
      const div = document.createElement("div");
      div.className = "dup-group";
      const keyPreview = g.items[0].cells.filter(c => c).slice(0, 4).join(" / ");
      div.innerHTML = `<div class="dg-title">重复组 ${gi + 1}（${g.items.length} 条）：${esc(keyPreview)}</div>`;
      g.items.forEach((it, ii) => {
        const row = document.createElement("div");
        row.className = "dup-item";
        row.dataset.g = gi; row.dataset.i = ii; row.dataset.remove = it.remove;
        row.innerHTML = `
          <span class="cells">[${esc(it.sheetLabel)}] ${esc(it.cells.filter(c => c).join(" | "))}</span>
          <button class="${it.remove ? "" : "on-keep"}" data-v="keep">不重复</button>
          <button class="${it.remove ? "on-del" : ""}" data-v="del">重复（删除）</button>`;
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
    toast(removedN ? `已标记删除 ${removedN} 条，剩余 ${data.total} 人` : `无重复项，共 ${data.total} 人`);
    renderRoles();
    gotoStep("fields");
  } catch (err) { toast(err.message, 4000); }
};

// ---------------------------------------------------------------- 第3步 名单字段用途
const PURPOSE = [
  ["fill", "自动填入"],
  ["cat", "分类基础"],
  ["exclude", "排除依据"],
  ["none", "不使用"],
];
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
        <td>${esc(h || "（空列名）")} <small class="hint">第${i + 1}列</small></td>
        <td><select data-key="${esc(c.key)}" data-col="${i}">${PURPOSE.map(([v, t]) =>
          `<option value="${v}" ${p === v ? "selected" : ""}>${t}</option>`).join("")}</select></td>
        <td><input type="text" class="excl-val" data-key="${esc(c.key)}" data-col="${i}" value="${esc(role.excludeCol === i ? (role.excludeVal || "") : "")}" placeholder="列值＝此值时排除，如：外派" style="width:100%;${p === "exclude" ? "" : "visibility:hidden"}"></td>
      </tr>`;
    }).join("");
    const div = document.createElement("div");
    div.className = "sheet-block";
    div.innerHTML = `
      <div class="sh-title">${esc(c.label)}　<small class="hint">${c.count} 行数据</small></div>
      <table class="tbl"><tr><th>名单列</th><th>用途</th><th>排除值</th></tr>${rows}</table>`;
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
      <input type="text" class="mg-from" value="${esc(m[0])}" placeholder="原分类名，如：维修组" style="max-width:220px;">
      <span>→ 并入 →</span>
      <input type="text" class="mg-into" value="${esc(m[1])}" placeholder="目标分类名，如：维修班" style="max-width:220px;">
      <button class="small" onclick="this.parentElement.remove()">删除</button>`;
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
    const t = row.querySelector(".mg-into").value.trim();
    if (f && t) merges.push([f, t]);
  });
  // 校验（只针对勾选参与的工作表；错误具体到表名并醒目显示）
  const chosenKeys = new Set(S.chosen.map(c => c.key));
  const bad = Object.keys(roles).filter(k => chosenKeys.has(k) && roles[k].fillCols.length === 0);
  if (bad.length) {
    const label = bad.map(k => {
      const c = S.columns.find(c => c.key === k);
      return c ? "「" + c.label + "」" : k;
    }).join("、");
    showErr("下面工作表还没有设置「自动填入」列（至少要把姓名列设为自动填入）：" + label);
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
      const name = c.headers[col] || `第${col + 1}列`;
      if (!set.includes(name)) set.push(name);
    });
  });
  S.fillFields = set;
}

// ---------------------------------------------------------------- 第4步 对应关系
const CONTROL_LABEL = { text: "文本", longtext: "多行文本", list: "多条内容", persons: "多人(最多5)", select: "下拉选择", location: "多条地点(最多3)", date: "日期", time: "时间", auto: "自动生成" };
const SOURCE_LABEL = [["fill", "用户填写"], ["auto", "自动生成"], ["roster", "由名单提供"], ["skip", "忽略"]];
function fieldOptions(sel, withBlank, blankText) {
  let html = withBlank ? `<option value="">${blankText || "（留空手写）"}</option>` : "";
  html += S.fillFields.map(f => `<option value="${esc(f)}" ${f === sel ? "selected" : ""}>${esc(f)}</option>`).join("");
  return html;
}
function renderMapping() {
  // 上：数据区列映射
  const mbox = $("mapping-list");
  mbox.innerHTML = "";
  const dc = S.tpl && S.tpl.dataCols ? S.tpl.dataCols : [];
  if (!dc.length) {
    mbox.innerHTML = "<p class='hint'>该模板未识别出数据区可映射列。</p>";
  }
  // 姓名列不参与映射（姓名由生成器自动依次填入，映射会导致同一人名复制到多个姓名格）
  S._mapping = (S._mapping || []).filter(m => !dc.find(c => c.colIdx === m.colIdx && c.isName));
  if (!S._mapping.length) S._mapping = defaultMapping(dc);
  dc.forEach(col => {
    const div = document.createElement("div");
    div.className = "form-row";
    if (col.isName) {
      div.innerHTML = `
        <label>${esc(col.label)} <b>（姓名列）</b> <small class="hint">列 ${col.colIdx}</small></label>
        <span class="hint">自动依次填入不同人员，无需映射</span>`;
      mbox.appendChild(div);
      return;
    }
    const cur = (S._mapping.find(m => m.colIdx === col.colIdx) || {}).field || "";
    div.innerHTML = `
      <label>${esc(col.label)} <small class="hint">列 ${col.colIdx}</small></label>
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
    box.innerHTML = "<p class='hint'>该模板没有识别出信息字段。签到表将只填人员名单。<br>若模板里有需要填写的栏目，请确认其为「标签格＋空白格」样式，或直接在第⑤步检查。</p>";
    return;
  }
  box.innerHTML = "";
  S.fields.forEach((f, i) => {
    const row = document.createElement("div");
    row.className = "design-row v2";
    row.innerHTML = `
      <span class="dn"><input type="text" class="fname" data-i="${i}" value="${esc(f.name)}" style="width:100%;">
        <small>${f.kind === "blank" ? "留空字段" : "占位符"}${f.default ? " · 预填：" + esc(f.default.slice(0, 12)) : ""}</small></span>
      <select data-i="${i}" data-k="mode">
        ${SOURCE_LABEL.map(([v, t]) => `<option value="${v}" ${f.mode === v ? "selected" : ""}>${t}</option>`).join("")}
      </select>
      <select data-i="${i}" data-k="rosterField" ${f.mode === "roster" ? "" : "hidden"}>${fieldOptions(f.rosterField, false)}</select>
      <select data-i="${i}" data-k="control" ${f.mode === "fill" ? "" : "hidden"}>
        ${Object.entries(CONTROL_LABEL).map(([k, v]) => `<option value="${k}" ${f.control === k ? "selected" : ""}>${v}</option>`).join("")}
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
    addBtn.textContent = "＋ 加一条";
    if (max) addBtn.textContent = `＋ 加一条（${count()}/${max}）`;
    addBtn.disabled = max ? count() >= max : false;
  };
  const addRow = (val) => {
    const r = document.createElement("div");
    r.className = "fill-dynamic";
    r.innerHTML = `<input type="text" placeholder="${placeholder}"> <button type="button" class="small">－</button>`;
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
  if (!fillable.length) box.innerHTML = "<p class='hint'>没有需要手动填写的字段。</p>";
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
      case "list": cell.appendChild(dynInput("第几条内容")); break;
      case "persons": cell.appendChild(dynInput("姓名", 5)); break;
      case "location": cell.appendChild(dynInput("地点", 3)); break;
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
        w.appendChild(t1); w.appendChild(document.createTextNode(" 至 ")); w.appendChild(t2);
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
    row.innerHTML = `<label>单张签到表容量</label>
      <div>每张 <input type="text" id="cap-input" value="${S.tpl.capacity}" style="width:70px;"> 人
      <span class="hint">（模板最大 ${S.tpl.capacity} 人；超出自动拆成多张，填写信息每张都有）</span></div>`;
    box.appendChild(row);
  }
  // 自动/名单来源字段说明
  const autos = S.fields.filter(f => f.mode === "auto" || f.mode === "roster");
  if (autos.length) {
    const p = document.createElement("p");
    p.className = "hint";
    p.innerHTML = "自动处理：" + autos.map(f =>
      esc(f.name) + (f.mode === "roster" ? "（←名单字段：" + esc(f.rosterField || "未选") + "）"
        : f.name.includes("人数") ? (S.remainTotal != null ? `（= ${S.remainTotal} 人）` : "") : "")
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
  } catch (err) { showErr("填写信息读取失败：" + err.message); return; }
  const cap = +($("cap-input") ? $("cap-input").value : 0);
  const btn = $("gen-btn");
  btn.disabled = true; btn.textContent = "正在生成……";
  try {
    const data = await post("/api/generate", { fill, capacity: cap });
    S.outputs = data.outputs || [];
    S.hasCat = !!data.hasCat;
    renderPreview();
    gotoStep("preview");
  } catch (err) { showErr(err.message); }
  btn.disabled = false; btn.textContent = "生成并预览 ↗";
};

// ---------------------------------------------------------------- 第6步 预览
function renderPreview() {
  $("preview-summary").innerHTML =
    `共生成 <b>${S.outputs.length}</b> 张签到表，填写 <b>${S.outputs.reduce((a, o) => a + o.count, 0)}</b> 人次。请逐张检查，确认后进入输出。`;
  const list = $("preview-list");
  list.innerHTML = "";
  S.outputs.forEach((o) => {
    const block = document.createElement("div");
    block.className = "pv-block";
    block.innerHTML = `
      <div class="pv-head-bar">
        <span><b>${esc(o.file)}</b>　${o.group ? "分类：" + esc(o.group) + " · " : ""}第 ${o.part}/${o.parts} 张 · ${o.count} 人</span>
      </div>
      <div class="pv-body" data-f="${encodeURIComponent(o.file)}"><p class="hint">预览加载中……</p></div>`;
    list.appendChild(block);
    fetch("/api/preview?file=" + encodeURIComponent(o.file))
      .then(r => r.text())
      .then(html => { block.querySelector(".pv-body").innerHTML = html; })
      .catch(() => { block.querySelector(".pv-body").innerHTML = "<p class='hint'>预览加载失败</p>"; });
  });
}
$("to-output").onclick = () => { renderOutput(); gotoStep("output"); };

// ---------------------------------------------------------------- 第7步 输出
function renderOutput() {
  const total = S.outputs.reduce((a, o) => a + o.count, 0);
  $("output-summary").innerHTML =
    `共 <b>${new Set(S.outputs.map(o => o.group)).size}</b> 个分类，<b>${S.outputs.length}</b> 张签到表，填写 <b>${total}</b> 人次。文件为 Word 格式。`;
  const list = $("output-list");
  list.innerHTML = S.outputs.map(o => `
    <div class="out-row">
      <span class="out-name">${esc(o.file)}</span>
      <span class="hint">${o.group ? esc(o.group) + " · " : ""}第 ${o.part}/${o.parts} 张 · ${o.count} 人</span>
      <a class="button small" href="/api/download?file=${encodeURIComponent(o.file)}">下载 Word</a>
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
      <div class="sh-title">${esc(g || "未分类")}　<small class="hint">${items.length} 张 · 共 ${items.reduce((a, o) => a + o.count, 0)} 人</small></div>
      ${items.map(o => `
        <div class="out-row">
          <span class="out-name">${esc(o.file)}</span>
          <span class="hint">${o.count} 人</span>
          <a class="button small" href="/api/download?file=${encodeURIComponent(o.file)}">下载</a>
        </div>`).join("")}
      <div class="out-row"><span></span><span></span>
        <a class="button small primary" href="/api/download-group?group=${encodeURIComponent(g)}">打包下载该分类（zip）</a>
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
$("finish-btn").onclick = () => toast("本次生成完成。可点击「重做」开始新一轮。");
$("finish-btn2").onclick = () => toast("本次生成完成。可点击「重做」开始新一轮。");
$("cancel-btn").onclick = async () => {
  if (!confirm("确定取消本次生成？已填写的信息将清空。")) return;
  await post("/api/reset", {});
  window.location.reload();
};

// ---------------------------------------------------------------- 启动
gotoStep("input");
