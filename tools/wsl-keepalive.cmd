@echo off
echo [%DATE% %TIME%] keepalive start >> "%TEMP%\wslka.log"
wsl.exe -d Ubuntu-24.04 -e sh -c "service mysql start; exec sleep infinity" >> "%TEMP%\wslka.log" 2>&1
echo [%DATE% %TIME%] keepalive exit rc=%ERRORLEVEL% >> "%TEMP%\wslka.log"
