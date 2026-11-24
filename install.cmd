@echo off
rem Sulcata installer for Windows: runs install.ps1 without changing
rem the PowerShell execution policy. Arguments are passed through, e.g.
rem   install.cmd -Start
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
set SULCATA_INSTALL_EXIT=%ERRORLEVEL%
rem Keep the window open when started by a double-click from Explorer.
echo %CMDCMDLINE% | find /i "%~nx0" >nul && pause
exit /b %SULCATA_INSTALL_EXIT%
