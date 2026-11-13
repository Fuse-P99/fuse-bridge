@echo off
setlocal
REM ═══════════════════════════════════════════════════════════════════════════
REM  Signs build\bin\FuseBridge.exe with Azure Trusted Signing.
REM  Call from build.bat AFTER the wails build, BEFORE commit/deploy:
REM      call signing\sign.bat || exit /b 1
REM
REM  One-time machine setup:
REM    1. winget install Microsoft.DotNet.Runtime.8    (the signing dlib needs it)
REM    2. winget install Microsoft.AzureCLI
REM       az login                                     (account with the
REM                                                    "Trusted Signing
REM                                                    Certificate Profile
REM                                                    Signer" role)
REM    3. Fill in the three TS_ values below once Azure identity validation is
REM       approved and the certificate profile exists. These are identifiers,
REM       not secrets — this file is safe to commit.
REM
REM  signtool + the Trusted Signing dlib download themselves into
REM  signing\tools\ on first run (gitignored).
REM ═══════════════════════════════════════════════════════════════════════════

REM ── Azure Trusted Signing account ──
set "TS_ACCOUNT=FuseBridge"
set "TS_PROFILE=FuseBridge"
REM Endpoint must match the account's region:
REM   East US:         https://eus.codesigning.azure.net
REM   West US 2:       https://wus2.codesigning.azure.net
REM   West Central US: https://wcus.codesigning.azure.net
REM   North Europe:    https://neu.codesigning.azure.net
REM   West Europe:     https://weu.codesigning.azure.net
set "TS_ENDPOINT=https://eus.codesigning.azure.net"

REM Until the TS_ values are filled in, warn and pass — the signing setup must
REM not block releases while Azure identity validation is still pending. Once
REM configured, any signing failure below is a hard stop.
if "%TS_ACCOUNT%"=="FILL_IN_ACCOUNT_NAME" (
    echo [sign] *************************************************************
    echo [sign] NOT CONFIGURED - this build ships UNSIGNED. Fill in the TS_
    echo [sign] values in signing\sign.bat once identity validation approves.
    echo [sign] *************************************************************
    exit /b 0
)

set "TOOLS=%~dp0tools"
set "EXE=%~dp0..\build\bin\FuseBridge.exe"
if not exist "%EXE%" (
    echo [sign] %EXE% not found — run the build first
    exit /b 1
)
if not exist "%TOOLS%" mkdir "%TOOLS%"

where dotnet >nul 2>&1
if errorlevel 1 (
    echo [sign] .NET runtime not found — run: winget install Microsoft.DotNet.Runtime.8
    exit /b 1
)
REM Signing authenticates through the Azure CLI's cached az-login session. A
REM console (or IDE terminal) opened before the CLI was installed has a stale
REM PATH and can't see az, which made the credential chain fall through to a
REM surprise browser popup — so add the CLI's standard install dir ourselves.
where az >nul 2>&1
if errorlevel 1 set "PATH=%PATH%;C:\Program Files\Microsoft SDKs\Azure\CLI2\wbin"
where az >nul 2>&1
if errorlevel 1 (
    echo [sign] WARNING: Azure CLI not found — install it and run: az login
)

REM ── bootstrap the Trusted Signing client (the signtool dlib) ──
set "DLIB=%TOOLS%\tsclient\bin\x64\Azure.CodeSigning.Dlib.dll"
if not exist "%DLIB%" (
    echo [sign] downloading Microsoft.Trusted.Signing.Client...
    powershell -NoProfile -ExecutionPolicy Bypass -Command "[Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12; Invoke-WebRequest 'https://www.nuget.org/api/v2/package/Microsoft.Trusted.Signing.Client' -OutFile '%TEMP%\tsclient.zip'; Expand-Archive '%TEMP%\tsclient.zip' '%TOOLS%\tsclient' -Force"
)
if not exist "%DLIB%" (
    echo [sign] FAILED to fetch the Trusted Signing client
    exit /b 1
)

REM ── bootstrap a current signtool (the installed 2020 SDK is too old) ──
set "SIGNTOOL="
for /d %%d in ("%TOOLS%\sdktools\bin\10.*") do set "SIGNTOOL=%%d\x64\signtool.exe"
if not defined SIGNTOOL (
    echo [sign] downloading Microsoft.Windows.SDK.BuildTools...
    powershell -NoProfile -ExecutionPolicy Bypass -Command "[Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12; Invoke-WebRequest 'https://www.nuget.org/api/v2/package/Microsoft.Windows.SDK.BuildTools' -OutFile '%TEMP%\sdktools.zip'; Expand-Archive '%TEMP%\sdktools.zip' '%TOOLS%\sdktools' -Force"
    for /d %%d in ("%TOOLS%\sdktools\bin\10.*") do set "SIGNTOOL=%%d\x64\signtool.exe"
)
if not defined SIGNTOOL (
    echo [sign] FAILED to fetch signtool
    exit /b 1
)

REM ── metadata handed to the dlib (regenerated each run so TS_ edits stick).
REM InteractiveBrowserCredential is excluded so a broken az session fails with
REM a readable error instead of popping a login browser mid-build. ──
> "%TOOLS%\metadata.json" echo { "Endpoint": "%TS_ENDPOINT%", "CodeSigningAccountName": "%TS_ACCOUNT%", "CertificateProfileName": "%TS_PROFILE%", "ExcludeCredentials": [ "InteractiveBrowserCredential" ] }

echo [sign] signing %EXE%
"%SIGNTOOL%" sign /fd SHA256 /tr "http://timestamp.acs.microsoft.com" /td SHA256 /dlib "%DLIB%" /dmdf "%TOOLS%\metadata.json" "%EXE%"
if errorlevel 1 (
    echo [sign] SIGNING FAILED — is az login done, and the identity validated?
    exit /b 1
)

"%SIGNTOOL%" verify /pa "%EXE%"
if errorlevel 1 (
    echo [sign] VERIFY FAILED
    exit /b 1
)
echo [sign] OK — FuseBridge.exe is signed
exit /b 0
