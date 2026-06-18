@echo off
REM Set these before building:
REM   SERVER_URL - the full URL to your server's /submit endpoint
REM   API_KEY    - the client_api_key from the server's config.json
set SERVER_URL=https://yourserver.com:8765/submit
set API_KEY=REPLACE_WITH_YOUR_KEY

REM Regenerate the Windows manifest resource (requires rsrc: go install github.com/akavel/rsrc@latest)
REM Only needed if app.manifest changes; rsrc.syso is already checked in.
REM %USERPROFILE%\go\bin\rsrc.exe -manifest app.manifest -o rsrc.syso

set GOOS=windows
set GOARCH=amd64
go build -ldflags "-X main.serverURL=%SERVER_URL% -X main.apiKey=%API_KEY% -H windowsgui" -o eq-relay.exe .
if %ERRORLEVEL% == 0 (
    echo Built eq-relay.exe successfully
) else (
    echo Build failed
)
