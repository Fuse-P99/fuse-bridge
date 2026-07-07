@echo off
REM Copy this file to build.bat and fill in the values below.
REM build.bat is gitignored so your API key stays out of the repo.
REM Requires the Wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0

set SERVER_URL=https://yourserver.com:5678/submit
set API_KEY=REPLACE_WITH_CLIENT_API_KEY_FROM_CONFIG_JSON

REM VERSION is read from settings.json — update settings.json when cutting a new release.
for /f "tokens=2 delims=:" %%v in ('findstr "version" settings.json') do set RAWVER=%%v
set VERSION=%RAWVER: =%
set VERSION=%VERSION:"=%
set VERSION=%VERSION:,=%

wails build -ldflags "-X main.serverURL=%SERVER_URL% -X main.apiKey=%API_KEY% -X main.clientVersion=%VERSION%"

if %ERRORLEVEL% == 0 (
    echo Built successfully: build\bin\FuseBridge.exe
) else (
    echo Build failed
)
