"""Exercise the bridge restart workflow with a simulated systemd invocation change."""

import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


WORKFLOW = Path(__file__).resolve().parents[2] / ".github/workflows/ops-restart-hosted-mcp.yml"
DEPLOY_LOCK = "/run/lock/faas-deploy.lock"

COMMANDS = {
    "flock": '[[ "$*" == "-n 9" ]]',
    "sleep": "exit 0",
    "systemctl": r'''
case "$1" in
  is-active) exit 0 ;;
  show) cat "$TEST_STATE/invocation" ;;
  restart)
    [[ "$*" == 'restart faas-mcp-bridge.service' ]] || exit 97
    printf 'first-id\n' > "$TEST_STATE/invocation"
    ;;
  *) exit 97 ;;
esac
''',
    "curl": r'''
[[ "$(cat "$TEST_STATE/invocation")" == first-id ]] || exit 1
if [[ "$REPLACE_DURING_PROBE" == yes ]]; then
  printf 'unready-id\n' > "$TEST_STATE/invocation"
fi
''',
}


class RestartHostedMCPTest(unittest.TestCase):
    def run_restart(self, replace_during_probe):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        remote = workflow.split("<<'REMOTE'\n", 1)[1].split("\n          REMOTE", 1)[0]
        script = textwrap.dedent(remote)
        self.assertIn(f"exec 9>>{DEPLOY_LOCK}", script)
        script = script.replace(DEPLOY_LOCK, '"$TEST_STATE/deploy.lock"')

        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            (work / "invocation").write_text("original-id\n", encoding="utf-8")
            for command, body in COMMANDS.items():
                executable = work / command
                executable.write_text(f"#!/bin/bash\nset -eu\n{body}\n", encoding="utf-8")
                executable.chmod(0o700)
            return subprocess.run(
                ["bash", "-s"], input=script, capture_output=True, text=True, timeout=15,
                env={**os.environ, "PATH": f"{work}:{os.environ['PATH']}", "TEST_STATE": directory,
                     "REPLACE_DURING_PROBE": "yes" if replace_during_probe else "no"},
            )

    def test_reports_the_invocation_that_answered(self):
        result = self.run_restart(replace_during_probe=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("changed from original-id to first-id", result.stdout)

    def test_rejects_an_unprobed_replacement(self):
        result = self.run_restart(replace_during_probe=True)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("Could not confirm a new bridge invocation", result.stderr)

    def test_preserves_queued_rollouts(self):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        concurrency = workflow.split("\nconcurrency:\n", 1)[1].split("\n\n", 1)[0]
        self.assertIn("  group: production-us-platform-rollout", concurrency.splitlines())
        self.assertIn("  queue: max", concurrency.splitlines())


if __name__ == "__main__":
    unittest.main()
