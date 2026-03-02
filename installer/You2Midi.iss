#define MyAppName "You2Midi"
#ifndef MyAppVersion
  #define MyAppVersion "0.1.0"
#endif
#ifndef SourceDir
  #define SourceDir "..\dist\desktop"
#endif

[Setup]
AppId={{8D85B931-35E5-4C37-A386-D2A9F13F021C}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
DefaultDirName={autopf}\You2Midi
DefaultGroupName=You2Midi
OutputBaseFilename=You2Midi-Setup-{#MyAppVersion}
Compression=lzma
SolidCompression=yes
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern

; Code-signing hook:
;   iscc /Ssigntool="signtool.exe sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 /a /f C:\cert.pfx /p secret `$f"
; If /Ssigntool is not provided, installer is generated unsigned.
#ifdef SignToolName
SignTool={#SignToolName}
#endif

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
Source: "{#SourceDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs; Excludes: "runtime\python\*;runtime\python.new\*;runtime\python.old\*;runtime\node\*;runtime\node.new\*;runtime\node.old\*"

[Icons]
Name: "{group}\You2Midi"; Filename: "{app}\You2midi.exe"
Name: "{commondesktop}\You2Midi"; Filename: "{app}\You2midi.exe"

[Run]
Filename: "{app}\runtime\vcredist\vc_redist.x64.exe"; Parameters: "/install /quiet /norestart"; StatusMsg: "Installing Microsoft Visual C++ Redistributable (x64)..."; Flags: runhidden waituntilterminated; Check: NeedInstallVCRedist
Filename: "{app}\runtime\webview2\MicrosoftEdgeWebView2Setup.exe"; Parameters: "/silent /install"; StatusMsg: "Installing Microsoft Edge WebView2 Runtime..."; Flags: runhidden waituntilterminated; Check: NeedInstallWebView2
Filename: "{app}\You2midi.exe"; Description: "Launch You2Midi"; Flags: nowait postinstall skipifsilent

[Code]
const
  VCRedistRegKey = 'SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64';
  VCRedistDownloadURL = 'https://aka.ms/vs/17/release/vc_redist.x64.exe';
  WebView2ClientGUID = '{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  WebView2DownloadURL = 'https://developer.microsoft.com/microsoft-edge/webview2/';

var
  NeedVCRedistInstall: Boolean;
  NeedWebView2Install: Boolean;

function VCRedistInstalled: Boolean;
var
  InstalledValue: Cardinal;
begin
  Result := False;

  if RegQueryDWordValue(HKLM64, VCRedistRegKey, 'Installed', InstalledValue) and (InstalledValue = 1) then
  begin
    Result := True;
    exit;
  end;

  if RegQueryDWordValue(HKLM32, VCRedistRegKey, 'Installed', InstalledValue) and (InstalledValue = 1) then
  begin
    Result := True;
    exit;
  end;
end;

function WebView2Installed: Boolean;
var
  VersionValue: string;
begin
  Result := False;

  if RegQueryStringValue(HKLM64, 'SOFTWARE\Microsoft\EdgeUpdate\Clients\' + WebView2ClientGUID, 'pv', VersionValue) and (VersionValue <> '') then
  begin
    Result := True;
    exit;
  end;

  if RegQueryStringValue(HKLM32, 'SOFTWARE\Microsoft\EdgeUpdate\Clients\' + WebView2ClientGUID, 'pv', VersionValue) and (VersionValue <> '') then
  begin
    Result := True;
    exit;
  end;

  if RegQueryStringValue(HKCU, 'SOFTWARE\Microsoft\EdgeUpdate\Clients\' + WebView2ClientGUID, 'pv', VersionValue) and (VersionValue <> '') then
  begin
    Result := True;
    exit;
  end;
end;

function NeedInstallVCRedist: Boolean;
begin
  Result := NeedVCRedistInstall;
end;

function NeedInstallWebView2: Boolean;
begin
  Result := NeedWebView2Install;
end;

procedure InitializeWizard;
begin
  NeedVCRedistInstall := not VCRedistInstalled;
  NeedWebView2Install := not WebView2Installed;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  InstalledAfterAttempt: Boolean;
  BootstrapperPath: string;
  ShellExecResultCode: Integer;
begin
  if CurStep <> ssPostInstall then
    exit;

  if NeedVCRedistInstall then
  begin
    InstalledAfterAttempt := VCRedistInstalled;
    if InstalledAfterAttempt then
    begin
      NeedVCRedistInstall := False;
    end
    else
    begin
      BootstrapperPath := ExpandConstant('{app}\runtime\vcredist\vc_redist.x64.exe');
      if FileExists(BootstrapperPath) then
      begin
        SuppressibleMsgBox(
          'Microsoft Visual C++ Redistributable (x64) installation did not complete.' + #13#10 +
          'Please install it manually, then launch You2Midi again.' + #13#10 + #13#10 +
          VCRedistDownloadURL,
          mbCriticalError,
          MB_OK,
          IDOK
        );
        ShellExec('open', VCRedistDownloadURL, '', '', SW_SHOWNORMAL, ewNoWait, ShellExecResultCode);
      end
      else
      begin
        SuppressibleMsgBox(
          'Microsoft Visual C++ Redistributable (x64) is required but the installer bootstrapper was not found.' + #13#10 +
          'Install it manually from:' + #13#10 + #13#10 +
          VCRedistDownloadURL,
          mbCriticalError,
          MB_OK,
          IDOK
        );
        ShellExec('open', VCRedistDownloadURL, '', '', SW_SHOWNORMAL, ewNoWait, ShellExecResultCode);
      end;
    end;
  end;

  if NeedWebView2Install then
  begin
    InstalledAfterAttempt := WebView2Installed;
    if InstalledAfterAttempt then
    begin
      NeedWebView2Install := False;
    end
    else
    begin
      BootstrapperPath := ExpandConstant('{app}\runtime\webview2\MicrosoftEdgeWebView2Setup.exe');
      if FileExists(BootstrapperPath) then
      begin
        SuppressibleMsgBox(
          'Microsoft Edge WebView2 Runtime installation did not complete.' + #13#10 +
          'Please install it manually, then launch You2Midi again.' + #13#10 + #13#10 +
          WebView2DownloadURL,
          mbCriticalError,
          MB_OK,
          IDOK
        );
        ShellExec('open', WebView2DownloadURL, '', '', SW_SHOWNORMAL, ewNoWait, ShellExecResultCode);
      end
      else
      begin
        SuppressibleMsgBox(
          'Microsoft Edge WebView2 Runtime is required but the installer bootstrapper was not found.' + #13#10 +
          'Install WebView2 Runtime manually from:' + #13#10 + #13#10 +
          WebView2DownloadURL,
          mbCriticalError,
          MB_OK,
          IDOK
        );
        ShellExec('open', WebView2DownloadURL, '', '', SW_SHOWNORMAL, ewNoWait, ShellExecResultCode);
      end;
    end;
  end;
end;
