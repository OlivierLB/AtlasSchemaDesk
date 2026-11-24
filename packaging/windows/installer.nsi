; Sulcata installer for Windows (per-user: no administrator rights,
; so in-app updates run silently). Built on Linux or Windows with:
;   makensis -DVERSION=x.y.z -DBINARY=path\sulcata.exe
;            -DWEBVIEW2=path\MicrosoftEdgeWebview2Setup.exe -DOUTFILE=setup.exe installer.nsi
; Silent mode (/S) is what the in-app update uses.

Unicode true
SetCompressor /SOLID lzma

!ifndef VERSION
  !error "Pass -DVERSION=x.y.z"
!endif
!ifndef BINARY
  !error "Pass -DBINARY=path to sulcata.exe"
!endif
!ifndef WEBVIEW2
  !error "Pass -DWEBVIEW2=path to MicrosoftEdgeWebview2Setup.exe"
!endif
!ifndef OUTFILE
  !define OUTFILE "sulcata_windows_amd64_setup.exe"
!endif

!define APP "Sulcata"
!define EXE "sulcata.exe"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Sulcata"
!define WEBVIEW2_KEY "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

Name "${APP}"
OutFile "${OUTFILE}"
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\${APP}"
ManifestDPIAware true
BrandingText "${APP} ${VERSION}"

VIProductVersion "${VERSION}.0"
VIFileVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "FileDescription" "Installation de ${APP}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "MIT License"

!include "MUI2.nsh"
!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Lancer ${APP}"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "French"

; WebView2 ships with Windows 11 and current Windows 10; install it otherwise.
Function EnsureWebView2
  ReadRegStr $0 HKLM "${WEBVIEW2_KEY}" "pv"
  StrCmp $0 "" 0 +2
  ReadRegStr $0 HKCU "${WEBVIEW2_KEY}" "pv"
  StrCmp $0 "" missing
  StrCmp $0 "0.0.0.0" missing
  DetailPrint "WebView2 présent ($0)."
  Return
missing:
  DetailPrint "Installation de Microsoft WebView2…"
  SetOutPath "$PLUGINSDIR"
  File "/oname=MicrosoftEdgeWebview2Setup.exe" "${WEBVIEW2}"
  ExecWait '"$PLUGINSDIR\MicrosoftEdgeWebview2Setup.exe" /silent /install' $1
  DetailPrint "Installation de WebView2 terminée (code $1)."
FunctionEnd

Section "Install"
  SetOutPath "$INSTDIR"
  ; During an update the previous version may still be closing.
  StrCpy $1 0
retry:
  ClearErrors
  Delete "$INSTDIR\${EXE}"
  IfErrors 0 ready
  IntOp $1 $1 + 1
  IntCmp $1 40 ready
  Sleep 500
  Goto retry
ready:
  File "/oname=${EXE}" "${BINARY}"
  File "/oname=LICENSE.txt" "..\..\LICENSE"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateShortcut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\${EXE}"
  ; A silent update must not recreate a desktop shortcut the user removed.
  IfSilent +2
  CreateShortcut "$DESKTOP\${APP}.lnk" "$INSTDIR\${EXE}"

  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${APP}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${APP}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${EXE}"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1

  Call EnsureWebView2
SectionEnd

; The configuration (%APPDATA%\sulcata) is kept on purpose.
Section "Uninstall"
  ; The application must be closed: otherwise its executable cannot go and
  ; the shortcuts and registration must stay.
  StrCpy $1 0
uretry:
  ClearErrors
  Delete "$INSTDIR\${EXE}"
  IfErrors 0 udeleted
  IntOp $1 $1 + 1
  IntCmp $1 20 ublocked
  Sleep 500
  Goto uretry
ublocked:
  MessageBox MB_OK|MB_ICONSTOP "Fermez ${APP} puis relancez la désinstallation." /SD IDOK
  SetErrorLevel 2
  Abort
udeleted:
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APP}.lnk"
  Delete "$DESKTOP\${APP}.lnk"
  DeleteRegKey HKCU "${UNINST_KEY}"
SectionEnd
