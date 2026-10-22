@echo off
rem Starts Claude Bridge in its own window (Windows). For autostart put a shortcut to this file in shell:startup.
cd /d "%~dp0"
if exist .venv\Scripts\python.exe (set PY=.venv\Scripts\python.exe) else (set PY=python)
start "Claude Bridge" %PY% claude_bridge.py %*
