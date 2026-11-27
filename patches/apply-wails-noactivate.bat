@echo off
REM Convenience wrapper: re-apply the Wails patches (V2 no-steal shows, V3
REM show() fallback-timer guard). Run this when build.bat fails its patch
REM guard or the build reports FuseBridgeSetNoStealShow / FuseBridgePatchV3
REM undefined. Safe to run any time -- it is idempotent and reports "Already
REM patched" when there is nothing to do.
REM
REM Full path to powershell.exe: some shells launch without System32 on PATH.
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "%~dp0apply-wails-noactivate.ps1"
exit /b %ERRORLEVEL%
