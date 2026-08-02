@echo off
REM Copy this file to build.bat and fill in the values below.
REM Requires the Wails v3 CLI: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.117
REM (build.bat installs it automatically if missing — keep the version pinned to
REM the wails/v3 version in go.mod so the CLI, Go library, and npm runtime match.)

set SERVER_URL=https://yourserver.com:5678/submit

REM VERSION is read from settings.json — update settings.json when cutting a new release.
for /f "tokens=2 delims=:" %%v in ('findstr "version" settings.json') do set RAWVER=%%v
set VERSION=%RAWVER: =%
set VERSION=%VERSION:"=%
set VERSION=%VERSION:,=%

REM 1. Generate JS bindings for the App service (static analysis of the Go code).
wails3 generate bindings
if %ERRORLEVEL% neq 0 ( echo Binding generation failed. & exit /b 1 )

REM 2. Build the frontend (needs the bindings from step 1).
pushd frontend
call npm install
if %ERRORLEVEL% neq 0 ( popd & echo npm install failed. & exit /b 1 )
call npm run build
if %ERRORLEVEL% neq 0 ( popd & echo Frontend build failed. & exit /b 1 )
popd

REM 3. Generate the Windows resource file (exe icon + manifest).
REM    -arch is REQUIRED: without it wails3 (alpha2.117) silently writes an
REM    empty .syso and exits 0, producing an exe with no icon.
wails3 generate syso -arch amd64 -manifest app.manifest -icon FuseIcon2.ico -out rsrc_windows_amd64.syso
if %ERRORLEVEL% neq 0 ( echo Syso generation failed. & exit /b 1 )
for %%A in (rsrc_windows_amd64.syso) do if %%~zA==0 ( echo Syso generation produced an empty file. & exit /b 1 )

REM 4. Compile (embeds frontend/dist; -H windowsgui = no console window).
go build -tags production -ldflags "-w -s -H windowsgui -X main.serverURL=%SERVER_URL% -X main.clientVersion=%VERSION%" -o build\bin\FuseBridge.exe .

if %ERRORLEVEL% == 0 (
    echo Built successfully: build\bin\FuseBridge.exe
) else (
    echo Build failed
)
