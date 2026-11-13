#!/usr/bin/env python3
"""Reproduce observed pipeline defects without changing product source files."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[4]
evidence = Path(__file__).resolve().parent
go = os.environ.get("GO_BINARY", "go")
with tempfile.TemporaryDirectory(prefix="gregale-pipeline-review-") as temp:
    overlay = Path(temp) / "overlay.json"
    replacements = {
        str(root / "pkg/datapipeline/investigation_probe_test.go"): str(evidence / "domain_probes.go.txt"),
        str(root / "cmd/gregale/investigation_probe_test.go"): str(evidence / "cli_probes.go.txt"),
    }
    overlay.write_text(json.dumps({"Replace": replacements}))
    command = [go, "test", "-overlay=" + str(overlay), "-trimpath", "-ldflags=-w",
               "-gcflags=github.com/onebox-faas/faas/cmd/gregale=-c=1 -dwarf=false",
               "-p", "1", "./pkg/datapipeline", "./cmd/gregale", "-run", "^TestInvestigation", "-count=1", "-v"]
    raise SystemExit(subprocess.call(command, cwd=root))
