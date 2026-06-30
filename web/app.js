"use strict";

const $ = (s, r = document) => r.querySelector(s);
const el = (tag, attrs = {}, ...kids) => {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") n.className = v;
    else if (k === "html") n.innerHTML = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined) n.setAttribute(k, v);
  }
  for (const k of kids) n.append(k?.nodeType ? k : document.createTextNode(k ?? ""));
  return n;
};
const esc = (s) => (s ?? "").replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));

async function api(path, opts) {
  const r = await fetch(path, opts);
  if (r.status === 401) { showLogin(); throw new Error("需要登录"); }
  const ct = r.headers.get("content-type") || "";
  const data = ct.includes("json") ? await r.json() : await r.text();
  if (!r.ok) throw new Error((data && data.error) || r.statusText);
  return data;
}

function showLogin() { $("#loginModal").classList.remove("hidden"); setTimeout(() => $("#loginPw")?.focus(), 50); }
async function doLogin() {
  $("#loginErr").textContent = "";
  try {
    const r = await fetch("/api/login", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ password: $("#loginPw").value }) });
    if (!r.ok) { $("#loginErr").textContent = "口令错误"; return; }
    location.reload();
  } catch (e) { $("#loginErr").textContent = "登录失败:" + e.message; }
}

const STATUS_TXT = { uploaded: "待评分", pending: "排队中", transcribing: "转写中", grading: "批改中", done: "已完成", error: "出错" };

const state = { assignment: null, selectedEssay: null, poll: null };
const els = {}; // 持久 DOM 容器引用,避免每次交互重建整页(否则图片会全部重新请求)

/* ---------------- 作业列表 ---------------- */
async function showList() {
  stopPoll();
  state.assignment = null;
  const list = await api("/api/assignments");
  const app = $("#app");
  app.innerHTML = "";
  app.append(
    el("div", { class: "toolbar" },
      el("h1", {}, "我的作业"),
      el("button", { class: "primary", onclick: openNewModal }, "+ 新建作业")),
  );
  if (!list.length) {
    app.append(el("p", { class: "muted" }, "还没有作业。点右上角“新建作业”,填好题目后批量上传作文照片即可。"));
    return;
  }
  const grid = el("div", { class: "grid" });
  for (const a of list) {
    const done = a.essays.filter((e) => e.status === "done").length;
    grid.append(el("div", { class: "card", onclick: () => openAssignment(a.id) },
      el("button", { class: "del-btn", title: "删除作业", onclick: (ev) => { ev.stopPropagation(); deleteAssignment(a.id, a.title); } }, "✕"),
      el("div", { class: "title" }, a.title),
      el("div", { class: "snippet" }, a.prompt),
      el("div", { class: "meta" },
        el("span", {}, `${a.essays.length} 篇 · ${done} 已批`),
        el("span", {}, a.createdAt.slice(0, 10)))));
  }
  app.append(grid);
}

/* ---------------- 作业详情 ---------------- */
async function openAssignment(id) {
  const a = await api(`/api/assignments/${id}`);
  state.assignment = a;
  if (state.selectedEssay && !a.essays.find((e) => e.id === state.selectedEssay)) state.selectedEssay = null;
  renderAssignment();
  schedulePoll();
}

// renderAssignment 整页构建(仅在打开作业 / 列表结构变化 / 轮询时调用)。
// 选中、旋转、删除等交互改用下面的局部 paint,不重建网格,避免图片被重新请求。
function renderAssignment() {
  const a = state.assignment;
  const app = $("#app");
  app.innerHTML = "";
  app.append(el("span", { class: "back", onclick: showList }, "← 返回作业列表"));

  // 题目面板
  const promptPanel = el("div", { class: "panel" },
    el("h1", {}, a.title),
    el("h3", {}, "作文题目 / 材料"),
    el("div", { class: "prompt-box" }, a.prompt));
  if (a.guide && a.guide.trim()) {
    promptPanel.append(el("h3", {}, "评分参考"), el("div", { class: "prompt-box guide-box" }, a.guide));
  }
  promptPanel.append(el("div", { class: "chips" },
    ...a.dimensions.map((d) => el("span", { class: "chip" }, `${d.name} ${d.max}分`)),
    el("span", { class: "chip" }, `满分 ${a.fullMarks}`)));
  app.append(promptPanel);

  // 上传面板
  const uploadStatus = el("span", { class: "hint" });
  const uploadBatch = async (files) => {
    if (!files || !files.length) return;
    const fd = new FormData();
    for (const f of files) if (f.type.startsWith("image/")) fd.append("images", f);
    uploadStatus.textContent = `上传 ${files.length} 张…`;
    try {
      await api(`/api/assignments/${a.id}/essays-batch`, { method: "POST", body: fd });
      uploadStatus.textContent = `已添加 ${files.length} 篇`;
      await refresh();
    } catch (e) { uploadStatus.textContent = "失败:" + e.message; }
  };
  const fileInput = el("input", {
    type: "file", accept: "image/*", multiple: true, class: "dz-input", title: "点击选择照片",
    onchange: (ev) => { uploadBatch(ev.target.files); ev.target.value = ""; },
  });
  const dz = el("div", {
    class: "dropzone",
    ondragover: (e) => { e.preventDefault(); dz.classList.add("drag"); },
    ondragleave: () => dz.classList.remove("drag"),
    ondrop: (e) => { e.preventDefault(); dz.classList.remove("drag"); uploadBatch(e.dataTransfer.files); },
  }, el("div", { class: "dz-big" }, "📷 点击选择 / 拖拽照片到这里"),
     el("div", { class: "hint" }, "每张照片自动成为一篇,可一次多选。先上传,确认方向后再统一评分。"),
     fileInput);
  app.append(el("div", { class: "panel" }, el("h3", {}, "上传作文"), dz, uploadStatus));

  // 作文列表(持久容器)
  els.essaysWrap = el("div", { class: "essays" });
  els.countH = el("h3", { style: "margin:0" });
  els.gradeBtn = el("button", { class: "primary", onclick: gradeAll });
  els.insightsBtn = el("button", {
    onclick: async (ev) => {
      const btn = ev.currentTarget;
      btn.disabled = true; btn.textContent = "⏳ 归纳中…(约半分钟)";
      try { await api(`/api/assignments/${a.id}/insights`, { method: "POST" }); await refresh(); }
      catch (e) { alert("生成洞察失败:" + e.message); btn.disabled = false; btn.textContent = "📊 生成数据洞察"; }
    },
  });
  app.append(el("div", { class: "panel" },
    el("div", { class: "toolbar" }, els.countH, el("div", {}, els.gradeBtn, document.createTextNode(" "), els.insightsBtn)),
    el("div", { class: "hint", style: "margin-bottom:8px" }, "提示:逐张确认方向正立(歪的点缩略图右上角 ↻ 旋转),再点「开始评分」。"),
    els.essaysWrap));

  els.detailPane = el("div", {});
  els.insightsPane = el("div", {});
  app.append(els.detailPane, els.insightsPane);

  els.detailKey = undefined; // 强制首绘
  paintEssays();
  paintDetail();
  paintInsights();
}

// paintEssays 重建作文网格(仅当列表结构/状态变化:上传、轮询)。
function paintEssays() {
  const a = state.assignment;
  els.essaysWrap.innerHTML = "";
  if (a.essays.length) {
    for (const e of a.essays) els.essaysWrap.append(essayPill(e));
  } else {
    els.essaysWrap.append(el("p", { class: "muted" }, "还没有作文,先在上方上传。"));
  }
  updateToolbar();
}

// updateToolbar 只更新计数与按钮文案(不动网格)。
function updateToolbar() {
  const a = state.assignment;
  els.countH.textContent = `作文(${a.essays.length})`;
  const pending = a.essays.filter((e) => e.status === "uploaded" || e.status === "error").length;
  els.gradeBtn.textContent = pending ? `▶ 开始评分(${pending} 篇)` : "▶ 开始评分";
  els.gradeBtn.disabled = !pending;
  els.insightsBtn.textContent = a.insights ? "🔄 重新生成洞察" : "📊 生成数据洞察";
  els.insightsBtn.disabled = false;
}

// paintDetail 只刷新详情面板;内容无变化时跳过(避免重拉详情大图)。
function paintDetail() {
  const e = state.selectedEssay && state.assignment.essays.find((x) => x.id === state.selectedEssay);
  const key = e ? `${e.id}:${e.status}:${e.rev || 0}:${(e.transcript || "").length}` : "";
  if (key === els.detailKey) return;
  els.detailKey = key;
  els.detailPane.innerHTML = "";
  if (e) els.detailPane.append(essayDetail(e));
}

// paintInsights 只刷新洞察面板。
function paintInsights() {
  els.insightsPane.innerHTML = "";
  if (state.assignment.insights) els.insightsPane.append(insightsPanel(state.assignment.insights));
}

// selectEssay 选中某篇:仅切换高亮 + 刷新详情,不重建网格(图片不被销毁,不会重新请求)。
function selectEssay(id) {
  state.selectedEssay = id;
  els.essaysWrap.querySelectorAll(".essay-pill.sel").forEach((n) => n.classList.remove("sel"));
  const cur = els.essaysWrap.querySelector(`[data-eid="${id}"]`);
  if (cur) cur.classList.add("sel");
  paintDetail();
}

function essayPill(e) {
  const sel = e.id === state.selectedEssay;
  const thumb = e.images && e.images.length
    ? el("div", { class: "thumb-wrap" },
        el("img", { class: "thumb", loading: "lazy", src: `/thumb/${e.images[0]}?v=${e.rev || 0}`, alt: "" }),
        el("button", { class: "rot-btn", title: "旋转 90°", onclick: (ev) => { ev.stopPropagation(); rotateEssay(e.id); } }, "↻"),
        el("button", { class: "del-btn-sm", title: "删除这篇", onclick: (ev) => { ev.stopPropagation(); deleteEssay(e.id, e.label); } }, "✕"))
    : "";
  return el("div", { class: "essay-pill" + (sel ? " sel" : ""), "data-eid": e.id, onclick: () => selectEssay(e.id) },
    thumb, el("div", { class: "lab" }, e.label || "(未命名)"), statusEl(e));
}

// statusEl 单独构建卡片的状态/分数区,便于轮询时只替换它(不动缩略图)。
function statusEl(e) {
  const spin = ["pending", "transcribing", "grading"].includes(e.status);
  return e.status === "done"
    ? el("div", { class: "st" }, el("span", { class: "score-big" }, String(e.total)), ` / ${state.assignment.fullMarks}`)
    : el("div", { class: "st st-" + e.status, html: (spin ? '<span class="spinner"></span>' : "") + (STATUS_TXT[e.status] || e.status) });
}

async function rotateEssay(id) {
  try {
    const updated = await api(`/api/essays/${id}/rotate`, { method: "POST" });
    const arr = state.assignment.essays;
    const i = arr.findIndex((x) => x.id === id);
    if (i >= 0) arr[i] = updated;
    state.assignment.insights = null; // 旋转后洞察失效(后端已清)
    // 只替换这一篇的卡片(它的图确实变了,只有它会重新请求);其它卡片节点不动 → 不重新请求
    const oldPill = els.essaysWrap.querySelector(`[data-eid="${id}"]`);
    if (oldPill) {
      const fresh = essayPill(updated);
      if (id === state.selectedEssay) fresh.classList.add("sel");
      oldPill.replaceWith(fresh);
    }
    updateToolbar();
    paintInsights();
    if (id === state.selectedEssay) paintDetail();
  } catch (e) { alert("旋转失败:" + e.message); }
}
async function deleteEssay(id, label) {
  if (!confirm(`确定删除作文「${label || "未命名"}」?不可恢复。`)) return;
  try {
    await api(`/api/essays/${id}`, { method: "DELETE" });
    const arr = state.assignment.essays;
    const i = arr.findIndex((x) => x.id === id);
    if (i >= 0) arr.splice(i, 1);
    state.assignment.insights = null;
    if (state.selectedEssay === id) state.selectedEssay = null;
    // 只移除这一个卡片节点,其它不动
    const node = els.essaysWrap.querySelector(`[data-eid="${id}"]`);
    if (node) node.remove();
    if (!arr.length) paintEssays(); // 变空了才显示占位
    updateToolbar();
    paintInsights();
    paintDetail();
  } catch (e) { alert("删除失败:" + e.message); }
}
async function deleteAssignment(id, title) {
  if (!confirm(`确定删除作业「${title}」?其下所有作文与批改结果都会一并删除,不可恢复。`)) return;
  try { await api(`/api/assignments/${id}`, { method: "DELETE" }); await showList(); }
  catch (e) { alert("删除失败:" + e.message); }
}
async function gradeAll() {
  try { await api(`/api/assignments/${state.assignment.id}/grade`, { method: "POST" }); await refresh(); }
  catch (e) { alert(e.message); }
}

function essayDetail(e) {
  const a = state.assignment;
  // 左:原始照片(点击放大)+ 旋转按钮
  const left = el("div", { class: "imgs" }, el("h3", {}, "原始照片"));
  // 详情用中等尺寸(~1100px,百来 KB),点击经 <a> 打开全图
  for (const rel of e.images) left.append(el("a", { href: "/" + rel, target: "_blank" }, el("img", { src: `/thumb/${rel}?w=1100&v=${e.rev || 0}`, alt: "作文照片" })));
  left.append(el("button", { class: "ghost", onclick: () => rotateEssay(e.id) }, "↻ 旋转 90°"));

  // 待评分:只让老师核对方向,不显示转写/评分
  if (e.status === "uploaded") {
    return el("div", { class: "panel" },
      el("h2", {}, "作文详情 · " + (e.label || "(未命名)")),
      el("div", { class: "detail" }, left,
        el("div", { class: "transcript-col" },
          el("h3", {}, "待评分"),
          el("p", { class: "muted" }, "请确认照片方向正立(歪了就点「↻ 旋转 90°」),确认无误后点上方「开始评分」即可转写并批改。"))));
  }

  // 右:转写稿(可编辑)
  const ta = el("textarea", { rows: "16" }, e.transcript || "");
  const msg = el("span", { class: "hint" });
  const saveBtn = el("button", { onclick: async () => { await api(`/api/essays/${e.id}/transcript`, { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify({ transcript: ta.value }) }); msg.textContent = "已保存,可点“重新批改”据此重打分"; } }, "保存转写稿");
  const regradeBtn = el("button", { class: "primary", onclick: async () => { await api(`/api/essays/${e.id}/regrade`, { method: "POST" }); msg.textContent = "重新批改中…"; await refresh(); } }, "重新批改");
  const right = el("div", { class: "transcript-col" },
    el("h3", {}, "转写稿(可编辑)"),
    ta,
    el("div", { class: "row" }, saveBtn, regradeBtn, msg));

  // 整宽:批改结果
  const scores = el("div", { class: "scores" }, el("h3", {}, "批改结果"));
  if (e.status === "error") {
    scores.append(el("div", { class: "err" }, "批改出错:" + e.error));
  } else if (e.status !== "done") {
    scores.append(el("p", { class: "muted" }, (STATUS_TXT[e.status] || e.status) + "…"));
  } else {
    scores.append(el("div", { class: "overall" }, e.overall || ""));
    const tbl = el("table");
    for (const d of e.scores) {
      const pct = d.max ? Math.round((d.score / d.max) * 100) : 0;
      tbl.append(el("tr", {},
        el("td", { class: "dim" }, d.name),
        el("td", { class: "sc" }, el("b", {}, `${d.score}`), `/${d.max}`),
        el("td", {}, d.comment)));
    }
    tbl.append(el("tr", { class: "total" },
      el("td", { class: "dim" }, "总分"), el("td", { class: "sc" }, el("b", {}, `${e.total}`), `/${a.fullMarks}`), el("td", {}, "")));
    scores.append(tbl);
  }

  return el("div", { class: "panel" },
    el("h2", {}, "作文详情 · " + (e.label || "(未命名)")),
    el("div", { class: "detail" }, left, right),
    scores);
}

/* ---------------- 数据洞察 ---------------- */
function insightsPanel(ins) {
  const maxBucket = Math.max(1, ...Object.values(ins.distribution));
  const dist = el("div", {});
  for (const [band, n] of Object.entries(ins.distribution)) {
    dist.append(el("div", { class: "dist-bar" },
      el("span", { style: "width:120px" }, band),
      el("span", { class: "bar", style: `width:${(n / maxBucket) * 200}px` }),
      el("span", {}, `${n} 人`)));
  }
  const issues = el("div", {}, ...(ins.commonIssues || []).map((s) => el("div", { class: "issue" }, "• " + s)));
  const picks = el("div", {}, ...(ins.picks || []).map((p) =>
    el("div", { class: "pick" },
      el("span", { class: "tag tag-" + p.type }, p.type),
      el("b", {}, (p.label || "某篇") + ":"), " " + p.reason)));
  return el("div", { class: "panel insights" },
    el("h2", {}, "📊 数据洞察"),
    el("div", {},
      el("span", { class: "stat", html: `平均分 <b>${ins.avg.toFixed(1)}</b>` }),
      el("span", { class: "stat", html: `最高 <b>${ins.max}</b>` }),
      el("span", { class: "stat", html: `最低 <b>${ins.min}</b>` }),
      el("span", { class: "stat", html: `批改篇数 <b>${ins.count}</b>` }),
      el("span", { class: "stat", html: `最弱维度 <b>${ins.weakestDim || "-"}</b>` })),
    el("h3", {}, "分数分布"), dist,
    el("h3", {}, "共性问题"), issues,
    el("h3", {}, "讲评选篇"), picks);
}

/* ---------------- 轮询 / 增量同步 ---------------- */
async function refresh() {
  if (!state.assignment) return;
  const a = await api(`/api/assignments/${state.assignment.id}`);
  const prevId = state.assignment.id;
  state.assignment = a;
  if (!els.essaysWrap || prevId !== a.id) {
    renderAssignment();
  } else {
    syncEssays(a); // 就地更新:不重建已有卡片的缩略图(避免在认证下被重新请求)
  }
  schedulePoll();
}

// syncEssays 把服务器最新状态增量同步到网格:新增补卡片、消失的移除、
// 其余只替换状态/分数区,**保留缩略图节点**(不触发重新请求)。
function syncEssays(a) {
  const wrap = els.essaysWrap;
  const ph = wrap.querySelector("p.muted");
  if (ph && a.essays.length) ph.remove();
  wrap.querySelectorAll(".essay-pill").forEach((n) => {
    if (!a.essays.find((e) => e.id === n.dataset.eid)) n.remove();
  });
  for (const e of a.essays) {
    const pill = wrap.querySelector(`[data-eid="${e.id}"]`);
    if (!pill) { wrap.append(essayPill(e)); continue; }
    const st = pill.querySelector(".st");
    if (st) st.replaceWith(statusEl(e)); // 只换状态区,缩略图不动
  }
  if (!a.essays.length && !wrap.querySelector("p.muted")) {
    wrap.append(el("p", { class: "muted" }, "还没有作文,先在上方上传。"));
  }
  updateToolbar();
  paintDetail();
  paintInsights();
}
function schedulePoll() {
  stopPoll();
  const busy = state.assignment?.essays.some((e) => ["pending", "transcribing", "grading"].includes(e.status));
  if (busy) state.poll = setTimeout(refresh, 2500);
}
function stopPoll() { if (state.poll) { clearTimeout(state.poll); state.poll = null; } }

/* ---------------- 新建作业弹窗 ---------------- */
function dimRow(name = "", max = 15) {
  return el("div", { class: "dim-row" },
    el("input", { class: "dn", type: "text", value: name, placeholder: "维度名" }),
    el("input", { class: "dm", type: "number", value: max, min: "0" }),
    el("button", { class: "danger", onclick: (ev) => ev.target.closest(".dim-row").remove() }, "×"));
}
function openNewModal() {
  $("#naTitle").value = ""; $("#naPrompt").value = ""; $("#naGuide").value = ""; $("#naFull").value = 60;
  $("#naErr").textContent = ""; $("#naPromptStatus").textContent = "";
  const dims = $("#naDims"); dims.innerHTML = "";
  for (const d of [["立意", 15], ["结构", 15], ["语言", 15], ["内容", 15]]) dims.append(dimRow(d[0], d[1]));
  $("#newModal").classList.remove("hidden");
}
function closeNewModal() { $("#newModal").classList.add("hidden"); }

async function createAssignment() {
  const dimensions = [...$("#naDims").querySelectorAll(".dim-row")].map((r) => ({
    name: r.querySelector(".dn").value.trim(), max: parseInt(r.querySelector(".dm").value) || 0,
  })).filter((d) => d.name);
  const body = {
    title: $("#naTitle").value.trim(),
    prompt: $("#naPrompt").value.trim(),
    guide: $("#naGuide").value.trim(),
    fullMarks: parseInt($("#naFull").value) || 60,
    dimensions,
  };
  if (!body.prompt) { $("#naErr").textContent = "请填写或识别作文题目"; return; }
  try {
    const a = await api("/api/assignments", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
    closeNewModal();
    openAssignment(a.id);
  } catch (e) { $("#naErr").textContent = e.message; }
}

async function recognizePromptImg(file) {
  $("#naPromptStatus").textContent = "识别中…";
  const fd = new FormData(); fd.append("image", file);
  try {
    const r = await api("/api/recognize-prompt", { method: "POST", body: fd });
    $("#naPrompt").value = r.prompt;
    $("#naPromptStatus").textContent = "已识别,请核对";
  } catch (e) { $("#naPromptStatus").textContent = "识别失败:" + e.message; }
}

/* ---------------- 初始化 ---------------- */
$("#homeLink").addEventListener("click", showList);
$("#naCancel").addEventListener("click", closeNewModal);
$("#naCreate").addEventListener("click", createAssignment);
$("#naAddDim").addEventListener("click", () => $("#naDims").append(dimRow()));
$("#naPromptImg").addEventListener("change", (e) => { if (e.target.files[0]) recognizePromptImg(e.target.files[0]); });
$("#loginBtn").addEventListener("click", doLogin);
$("#loginPw").addEventListener("keydown", (e) => { if (e.key === "Enter") doLogin(); });
showList().catch(() => {});
