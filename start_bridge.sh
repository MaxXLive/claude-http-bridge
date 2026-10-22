#!/bin/sh
# Starts Claude Bridge in the foreground (macOS/Linux). For autostart use the files in service/.
cd "$(dirname "$0")" || exit 1
if [ -x .venv/bin/python ]; then PY=.venv/bin/python; else PY=python3; fi
exec "$PY" claude_bridge.py "$@"
