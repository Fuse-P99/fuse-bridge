@echo off
REM Set these before building:
REM   SERVER_URL - the full URL to your server's /submit endpoint
REM   API_KEY    - the client_api_key from the server's config.json
set SERVER_URL=http://178.156.252.0:5678/submit
set API_KEY=Fuse2026

set GOOS=windows
set GOARCH=amd64
go build -ldflags "-X main.serverURL=%SERVER_URL% -X main.apiKey=%API_KEY% -H windowsgui" -o eq-relay.exe .
if %ERRORLEVEL% == 0 (
    echo Built eq-relay.exe successfully
) else (
    echo Build failed
)
