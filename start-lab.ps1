# start-lab.ps1 - mockc2 lab instance launcher (MySQL inside WSL2).
#
# Why this exists (2026-09-30 incident):
#   MySQL runs inside WSL2. WSL auto-shuts-down when idle -> DB dial hangs
#   4-10s per write -> device registration fails, frontend times out with
#   "cannot connect" banner. This script keeps WSL+mysqld alive and resolves
#   the WSL eth0 IP dynamically (it drifts across reboots).
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File start-lab.ps1             # port 9101
#   powershell -ExecutionPolicy Bypass -File start-lab.ps1 -Port 9102
param([int]$Port = 9101, [int]$PprofPort = 6062)

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$exe  = Join-Path $root "mockc2-lab.exe"

if (-not (Test-Path $exe)) { Write-Error "exe not found: $exe"; exit 1 }

# -- 1) WSL keepalive + mysqld (idempotent) --
Write-Host "[i] ensuring WSL Ubuntu-24.04 running + mysqld started ..."
$keep = Get-CimInstance Win32_Process -Filter "Name='wsl.exe'" |
    Where-Object { $_.CommandLine -match "sleep infinity" }
if (-not $keep) {
    Start-Process -WindowStyle Hidden -FilePath "wsl.exe" `
        -ArgumentList "-d","Ubuntu-24.04","-e","sh","-c","service mysql start >/dev/null 2>&1; exec sleep infinity"
    Start-Sleep -Seconds 2
}

# -- 2) resolve WSL eth0 IP dynamically --
# (EAP=Stop makes redirected native stderr throw; relax locally)
$prevEAP = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$raw = & wsl -d Ubuntu-24.04 -e sh -c "ip -4 addr show eth0 2>/dev/null" 2>$null
$ErrorActionPreference = $prevEAP
$wslIP = $null
foreach ($line in $raw) {
    if ($line -match 'inet\s+(\d+\.\d+\.\d+\.\d+)') { $wslIP = $Matches[1]; break }
}
if (-not $wslIP) { Write-Error "cannot resolve WSL eth0 IP (WSL not running?)"; exit 1 }
Write-Host ("[i] WSL MySQL endpoint: " + $wslIP + ":3306")

# -- 3) DB reachability precheck (3 retries) --
$ok = $false
foreach ($i in 1..3) {
    $c = New-Object Net.Sockets.TcpClient
    try { $c.Connect($wslIP, 3306); $ok = $true; break } catch { Start-Sleep -Seconds 1 } finally { $c.Close() }
}
if (-not $ok) { Write-Error ("DB " + $wslIP + ":3306 unreachable, aborting (refuse to run degraded)"); exit 1 }
Write-Host "[i] DB reachable"

# -- 4) stop old instance on same port --
$conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
if ($conn) {
    $oldPid = ($conn | Select-Object -First 1).OwningProcess
    Write-Host "[i] stopping old instance pid=$oldPid ..."
    Stop-Process -Id $oldPid -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
}

# -- 5) start (db = mockc2_lab; creds match config defaults) --
$log = Join-Path $root "server-lab.log"
$env:MOCKC2_DB_HOST     = $wslIP
$env:MOCKC2_DB_PORT     = "3306"
$env:MOCKC2_DB_USER     = "mockc2"
$env:MOCKC2_DB_PASSWORD = "mockc2pass"
$env:MOCKC2_DB_NAME     = "mockc2_lab"
$env:MOCKC2_ROOT        = $root

Write-Host "[i] starting mockc2-lab :$Port (pprof :$PprofPort) log: server-lab.log"
$proc = Start-Process -WindowStyle Hidden -FilePath $exe `
    -ArgumentList "--no-https","--http-port",$Port,"--pprof-addr","127.0.0.1:$PprofPort" `
    -WorkingDirectory $root -RedirectStandardOutput $log -RedirectStandardError ($log + ".err") -PassThru

Start-Sleep -Seconds 3
if (Get-Process -Id $proc.Id -ErrorAction SilentlyContinue) {
    Write-Host "[OK] instance up pid=$($proc.Id)  http://127.0.0.1:$Port/admin"
    Write-Host "     pprof: http://127.0.0.1:$PprofPort/debug/pprof/  log: server-lab.log"
} else {
    Write-Error "start failed, see $log.err"
    Get-Content ($log + ".err") -ErrorAction SilentlyContinue | Select-Object -First 20
}
