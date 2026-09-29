@echo off
rem start.bat —— 以多 worker + 内置 LB 常驻运行（日志追加 server.log）
cd /d %~dp0
mockc2.exe --no-https --http-port 9100 --workers 4 >> server.log 2>&1
