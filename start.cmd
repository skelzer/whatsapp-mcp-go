@echo off
REM WhatsApp MCP - double-click launcher for Windows.
REM Runs the PowerShell setup script (Docker check, .env, compose up, wizard).
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\start.ps1"
