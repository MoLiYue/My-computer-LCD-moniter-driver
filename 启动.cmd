@echo off
cd /d "%~dp0"
if not exist "MikuScreenHub.exe" (
    echo Run build-go.ps1 first.
    pause
    exit /b 1
)
"%~dp0MikuScreenHub.exe" --start --open
pause
