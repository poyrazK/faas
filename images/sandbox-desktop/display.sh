# ADR-20261124075005 §3: login shells and the desktop's terminals reach the
# sandbox desktop's X display.
export DISPLAY="${DISPLAY:-:0}"
