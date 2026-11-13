#!/usr/bin/env python3
"""Load investigation probes through temporary Go overlays; product files stay unchanged."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

evidence = Path(__file__).resolve().parent
root = evidence.parents[3]
env = dict(os.environ, PIPELINE_INVESTIGATION_EVIDENCE=str(evidence))
with tempfile.TemporaryDirectory(prefix="gregale-pipeline-review-round2-") as temp:
    overlay = Path(temp) / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {
        str(root / "pkg/datapipeline/investigation_round2_test.go"): str(evidence / "domain_probes.go.txt"),
        str(root / "cmd/gregale/investigation_round2_test.go"): str(evidence / "cli_probes.go.txt"),
    }}))
    command = [env.get("GO_BINARY", "go"), "test", "-overlay=" + str(overlay),
               "-trimpath", "-ldflags=-w", "-gcflags=github.com/onebox-faas/faas/cmd/gregale=-c=1 -dwarf=false",
               "-p", "1", "./pkg/datapipeline", "./cmd/gregale", "-run", "^TestInvestigationRound2", "-count=1", "-v"]
    raise SystemExit(subprocess.call(command, cwd=root, env=env))
