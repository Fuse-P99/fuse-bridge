@echo off
REM Set these before building:
REM   SERVER_URL - the full URL to your server's /submit endpoint
REM   API_KEY    - the client_api_key from the server's config.json
REM   VERSION    - must match client_version in the server's config.json
set SERVER_URL=http://178.156.252.0:5678/submit
set API_KEY=Fuse2026
set VERSION=1.0.1

set GOOS=windows
set GOARCH=amd64
rsrc -manifest app.manifest -ico FuseIcon2.ico -o rsrc.syso
go build -ldflags "-X main.serverURL=%SERVER_URL% -X main.apiKey=%API_KEY% -X main.clientVersion=%VERSION% -H windowsgui" -o "Fuse Bridge.exe" .
if %ERRORLEVEL% == 0 (
    echo Built "Fuse Bridge.exe" successfully ^(version %VERSION%^)
) else (
    echo Build failed
)
