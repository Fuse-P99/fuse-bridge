@echo off
REM Copy this file to build.bat and fill in the values below.
REM build.bat is gitignored so your API key stays out of the repo.

set SERVER_URL=https://yourserver.com:8765/submit
set API_KEY=REPLACE_WITH_CLIENT_API_KEY_FROM_CONFIG_JSON

set GOOS=windows
set GOARCH=amd64
go build -ldflags "-X main.serverURL=%SERVER_URL% -X main.apiKey=%API_KEY% -X main.clientVersion=%VERSION% -H windowsgui" .
if %ERRORLEVEL% == 0 (
    echo Built FuseBridge.exe successfully
) else (
    echo Build failed
)
