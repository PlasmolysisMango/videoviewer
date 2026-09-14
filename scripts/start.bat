@echo off
echo Starting VideoViewer...
start "" "javdbserver.exe" -addr 127.0.0.1:18888
timeout /t 2 /nobreak >nul
start "" "videoviewer.exe"
exit
