@echo off
REM Convenience wrapper: re-apply the Wails no-activate show patch.
REM Run this when build.bat fails its patch guard. Safe to run any time --
REM it is idempotent and reports "Already patched" when there is nothing to do.
REM
REM Full path to powershell.exe: some shells launch without System32 on PATH.
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "%~dp0apply-wails-noactivate.ps1"
exit /b %ERRORLEVEL%
