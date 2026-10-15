# start-dev.ps1 — mockc2 本地联调一键拉起（Windows 主机 + WSL2 MySQL）
#
# 背景坑（2026-10-03 实测）：
#   1) WSL2 在所有前台 wsl.exe 命令结束后约 20s 自动关机 -> mysqld 随之退出
#      -> 后端 mysql ping 报 "unexpected EOF" / "actively refused"。
#      解决：用一个 detached 的 `wsl -e sh -c "...; exec sleep <huge>"` 常驻保活。
#   2) `Start-Process -PassThru` 返回的是 launcher PID，真正干活的 wsl.exe 是子进程，
#      所以不要用 launcher PID 判断存活，改用端口探测。
#   3) 后端 db_host 用 WSL eth0 IP（NAT 模式 127.0.0.1 转发在部分配置下不可用）。
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File tools\start-dev.ps1
#   powershell -ExecutionPolicy Bypass -File tools\start-dev.ps1 -BackendPort 9100 -FrontendPort 5173
param(
  [int]$BackendPort  = 9100,
  [int]$FrontendPort = 5173,
  [string]$Distro    = "Ubuntu-24.04"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot   # go\
$fe   = Join-Path $root "frontend"

function Test-Tcp([string]$h, [int]$p, [int]$timeoutMs = 800) {
  $c = New-Object Net.Sockets.TcpClient
  try { $t = $c.BeginConnect($h, $p, $null, $null); if ($t.AsyncWaitHandle.WaitOne($timeoutMs)) { $c.EndConnect($t); return $true }; return $false }
  catch { return $false } finally { $c.Close() }
}

# -- 1) WSL keepalive + MySQL --
Write-Host "[1/4] ensuring WSL '$Distro' keepalive ..."
$keep = Get-CimInstance Win32_Process -Filter "Name='wsl.exe'" |
        Where-Object { $_.CommandLine -match "sleep 2147483647" }
if (-not $keep) {
  Start-Process -WindowStyle Hidden -FilePath "wsl.exe" `
    -ArgumentList "-d",$Distro,"-e","sh","-c","service mysql start >/dev/null 2>&1; exec sleep 2147483647"
}
Start-Sleep -Seconds 3

# resolve WSL eth0 IP (drifts across WSL restarts)
$prev = $ErrorActionPreference; $ErrorActionPreference = "Continue"
$raw = & wsl -d $Distro -e sh -c "ip -4 addr show eth0 2>/dev/null" 2>$null
$ErrorActionPreference = $prev
$wslIP = $null
foreach ($line in $raw) { if ($line -match 'inet\s+(\d+\.\d+\.\d+\.\d+)') { $wslIP = $Matches[1]; break } }
if (-not $wslIP) { Write-Error "cannot resolve WSL eth0 IP"; exit 1 }
Write-Host "      WSL MySQL endpoint: $wslIP:3306"

$ok = $false
foreach ($i in 1..10) { if (Test-Tcp $wslIP 3306) { $ok = $true; break }; Start-Sleep -Seconds 1 }
if (-not $ok) { Write-Error "MySQL $wslIP:3306 unreachable"; exit 1 }
Write-Host "      MySQL reachable OK"

# keep settings in sync with the resolved IP (db_host drifts)
$settingsPath = Join-Path $root "mockc2_settings.json"
$settings = Get-Content $settingsPath -Raw | ConvertFrom-Json
if ($settings.db_host -ne $wslIP) {
  $settings.db_host = $wslIP
  $settings | ConvertTo-Json -Depth 6 | Set-Content $settingsPath -Encoding UTF8
  Write-Host "      updated mockc2_settings.json db_host -> $wslIP"
}

# -- 2) backend --
Write-Host "[2/4] starting backend mockc2 :$BackendPort ..."
Get-Process -Name mockc2 -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1
$blog = Join-Path $root "server.dev.log"
Start-Process -WindowStyle Hidden -FilePath (Join-Path $root "mockc2.exe") `
  -ArgumentList "--no-https","--http-port",$BackendPort `
  -WorkingDirectory $root -RedirectStandardOutput $blog -RedirectStandardError ($blog + ".err")
$bok = $false
foreach ($i in 1..15) { if (Test-Tcp "127.0.0.1" $BackendPort) { $bok = $true; break }; Start-Sleep -Seconds 1 }
if (-not $bok) { Write-Error "backend did not come up; see $blog / $blog.err"; exit 1 }
Write-Host "      backend OK  http://127.0.0.1:$BackendPort/admin"

# -- 3) frontend dev server --
Write-Host "[3/4] starting frontend vite :$FrontendPort ..."
$flog = Join-Path $fe "dev.server.log"
Start-Process -FilePath "cmd.exe" -ArgumentList "/c","npm run dev" `
  -WorkingDirectory $fe -WindowStyle Hidden -RedirectStandardOutput $flog -RedirectStandardError (Join-Path $fe "dev.server.err.log")
$fok = $false
foreach ($i in 1..30) { if (Test-Tcp "127.0.0.1" $FrontendPort) { $fok = $true; break }; Start-Sleep -Seconds 1 }
if (-not $fok) { Write-Error "vite did not come up; see $flog"; exit 1 }
Write-Host "      frontend OK  http://127.0.0.1:$FrontendPort/"

Write-Host "[4/4] all up: frontend http://127.0.0.1:$FrontendPort/  backend http://127.0.0.1:$BackendPort/admin"
