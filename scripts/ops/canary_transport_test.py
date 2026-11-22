#!/usr/bin/env python3
"""Exercise the real canary shell helpers with local transport executables.

No production host, key, webhook or notification provider is contacted.
"""

import json
import os
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SSH = ROOT / "scripts/ops/canary_ssh.sh"
FAILURE = ROOT / "scripts/ops/canary_failure.sh"
PIN = "ssh-ed25519 AAAAPinnedKeyFixture"
SECRET = "private-credential-fixture"

STUB = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import subprocess
import sys

name, args = Path(sys.argv[0]).name, sys.argv[1:]
event = {"command": name, "args": args}
if name == "curl":
    data = args[args.index("--data") + 1]
    event["payload"] = sys.stdin.read() if data == "@-" else data
    if "--config" in args:
        config = Path(args[args.index("--config") + 1]).read_text()
        event["uses_private_config"] = config == f'url = "{os.environ["CANARY_FALLBACK_WEBHOOK_URL"]}"\n'
elif name == "ssh" and os.environ.get("CAPTURE_SSH_STDIN"):
    event["stdin"] = sys.stdin.read()
with open(os.environ["STUB_EVENTS"], "a") as output:
    output.write(json.dumps(event) + "\n")
if name == "timeout":
    if args[2] == os.environ.get("TIMEOUT_DURATION"):
        print(os.environ["PRIVATE_DIAGNOSTIC"], file=sys.stderr)
        sys.exit(124)
    sys.exit(subprocess.run(args[3:]).returncode)
if name == "ssh-keyscan":
    print(os.environ.get("SCAN_OUTPUT", ""), end="")
    print(os.environ["PRIVATE_DIAGNOSTIC"], file=sys.stderr)
    sys.exit(int(os.environ.get("SCAN_EXIT", "0")))
if name == "ssh":
    if args[-1] == "true":
        print(os.environ.get("PREFLIGHT_ERROR", ""), file=sys.stderr)
        sys.exit(int(os.environ.get("PREFLIGHT_EXIT", "0")))
    if os.environ.get("EXECUTE_REMOTE") == "1":
        sys.exit(subprocess.run(["bash", "-c", args[-1]]).returncode)
    print(os.environ["PRIVATE_DIAGNOSTIC"], file=sys.stderr)
    sys.exit(int(os.environ.get("SSH_EXIT", "0")))
if name == "curl":
    print(os.environ["PRIVATE_DIAGNOSTIC"], file=sys.stderr)
    if "--write-out" in args:
        print(os.environ.get("HTTP_STATUS", "204"), end="")
    sys.exit(int(os.environ.get("CURL_EXIT", "0")))
raise SystemExit("unexpected fixture command")
'''


class CanaryTransportTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="gregale-canary-transport-")
        self.addCleanup(directory.cleanup)
        self.directory = Path(directory.name)
        self.bin = self.directory / "bin"
        self.bin.mkdir()
        for command in ("timeout", "ssh-keyscan", "ssh", "curl"):
            target = self.bin / command
            target.write_text(STUB)
            target.chmod(0o700)
        self.events_file = self.directory / "events.jsonl"
        self.runner = self.directory / "runner"
        self.runner.mkdir()
        self.ssh_dir = self.runner / "canary-ssh"
        self.environ = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                            RUNNER_TEMP=str(self.runner), STUB_EVENTS=str(self.events_file),
                            CP_HOST="private-control-plane.invalid", CP_SSH_KEY=SECRET,
                            COMPUTE_KNOWN_HOSTS=f"fleet-node {PIN}\n",
                            SCAN_OUTPUT=f"private-control-plane.invalid {PIN}\n",
                            PRIVATE_DIAGNOSTIC=SECRET,
                            CANARY_FALLBACK_WEBHOOK_URL=f"https://chat.invalid/{SECRET}",
                            CANARY_FLEET="production-us", ACTIVE_RELEASE="a" * 40,
                            GITHUB_SERVER_URL="https://github.com", GITHUB_REPOSITORY="example/gregale",
                            GITHUB_RUN_ID="42", CANARY_PROBE_OUTCOME="skipped",
                            CANARY_HEARTBEAT_OUTCOME="skipped")

    def invoke(self, script, *arguments, **environment):
        return subprocess.run(["bash", str(script), *arguments], check=False,
                              env=dict(self.environ, **environment), input="",
                              capture_output=True, text=True, timeout=10)

    def events(self, command):
        if not self.events_file.exists():
            return []
        return [event for line in self.events_file.read_text().splitlines()
                if (event := json.loads(line))["command"] == command]

    def assert_setup(self, expected="ssh_ready", **environment):
        result = self.invoke(SSH, "setup", **environment)
        self.assertEqual(result.returncode, 0 if expected == "ssh_ready" else 1, result.stderr)
        self.assertEqual((self.ssh_dir / "status").read_text().strip(), expected)
        self.assertEqual((self.ssh_dir / "ready").exists(), expected == "ssh_ready")
        self.assertIn(expected, result.stdout + result.stderr)
        self.assertNotIn(SECRET, result.stdout + result.stderr)
        self.assertNotIn(self.environ["CP_HOST"], result.stdout + result.stderr)
        return result

    def test_configuration_errors_do_not_scan_or_authenticate(self):
        for variables, reason in [({"CP_HOST": ""}, "ssh_configuration_missing"),
                                  ({"CP_SSH_KEY": ""}, "ssh_configuration_missing"),
                                  ({"CP_HOST": "-oProxyCommand=bad"}, "ssh_host_invalid"),
                                  ({"CP_HOST": "host; echo bad"}, "ssh_host_invalid"),
                                  ({"COMPUTE_KNOWN_HOSTS": ""}, "ssh_host_pins_missing")]:
            with self.subTest(reason=reason, variables=variables):
                self.assert_setup(reason, **variables)
        self.assertEqual(self.events("ssh-keyscan"), [])
        self.assertEqual(self.events("ssh"), [])

    def test_an_empty_scan_is_unavailable_rather_than_a_key_mismatch(self):
        for status in ("0", "1"):
            with self.subTest(status=status):
                result = self.assert_setup("ssh_host_keys_unavailable", SCAN_OUTPUT="", SCAN_EXIT=status)
                self.assertNotIn("ssh_host_key_untrusted", result.stderr)
        self.assertEqual(self.events("ssh"), [])

    def test_the_whole_scan_has_a_deadline(self):
        self.assert_setup("ssh_host_scan_timeout", TIMEOUT_DURATION="15s")
        self.assertEqual(self.events("ssh-keyscan"), [])
        self.assertEqual(self.events("ssh"), [])

    def test_unknown_keys_and_marker_entries_cannot_establish_trust(self):
        pins = ["fleet-node ssh-ed25519 AAAAOtherKey", f"# fleet-node {PIN}", f"  # {PIN}",
                f"@revoked fleet-node {PIN}", f"@cert-authority fleet-node {PIN}"]
        for pin in pins:
            with self.subTest(pin=pin):
                self.assert_setup("ssh_host_key_untrusted", COMPUTE_KNOWN_HOSTS=pin)
        self.assertEqual(self.events("ssh"), [])

    def test_only_pinned_keys_are_written_and_all_files_are_private(self):
        self.assert_setup(COMPUTE_KNOWN_HOSTS=f"|1|hashed-host|hash {PIN}\n",
                          SCAN_OUTPUT=f"# scan diagnostic\nother-host ssh-rsa AAAAOtherKey\ncp {PIN}\n")
        self.assertEqual((self.ssh_dir / "known_hosts").read_text(),
                         f"{self.environ['CP_HOST']} {PIN}\n")
        self.assertEqual((self.ssh_dir / "deploy_key").read_text(), SECRET + "\n")
        self.assertEqual(stat.S_IMODE(self.ssh_dir.stat().st_mode), 0o700)
        for name in ("known_hosts", "deploy_key", "status", "ready"):
            self.assertEqual(stat.S_IMODE((self.ssh_dir / name).stat().st_mode), 0o600)

    def test_preflight_authentication_and_changed_key_errors_are_distinct(self):
        cases = [("Permission denied (publickey). " + SECRET, "ssh_authentication_failed"),
                 ("Host key verification failed. " + SECRET, "ssh_host_key_untrusted"),
                 ("Connection refused " + SECRET, "ssh_preflight_failed")]
        for error, expected in cases:
            with self.subTest(expected=expected):
                self.assert_setup(expected, PREFLIGHT_EXIT="255", PREFLIGHT_ERROR=error)
        self.assert_setup("ssh_preflight_timeout", TIMEOUT_DURATION="20s")

    def test_failed_setup_clears_previous_trust_and_run_refuses_it(self):
        self.assert_setup()
        self.assert_setup("ssh_host_keys_unavailable", SCAN_OUTPUT="")
        count = len(self.events("ssh"))
        result = self.invoke(SSH, "run", "true")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ssh_not_ready", result.stderr)
        self.assertEqual(len(self.events("ssh")), count)

    def test_shared_transport_preserves_script_stdin_and_uses_bounded_strict_options(self):
        self.assert_setup()
        result = subprocess.run(["bash", str(SSH), "run", "bash -s"], check=False,
                                env=dict(self.environ, CAPTURE_SSH_STDIN="1"),
                                input="echo customer-probe\n", text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(self.events("ssh")[-1]["stdin"], "echo customer-probe\n")
        for event in self.events("ssh"):
            args = event["args"]
            for option in ("BatchMode=yes", "IdentitiesOnly=yes", "ConnectTimeout=10",
                           "ConnectionAttempts=1", "StrictHostKeyChecking=yes",
                           "GlobalKnownHostsFile=/dev/null", "ServerAliveInterval=10",
                           "ServerAliveCountMax=2", f"UserKnownHostsFile={self.ssh_dir}/known_hosts"):
                self.assertIn(option, args)
            self.assertEqual(args[:2], ["-F", "/dev/null"])
        deadlines = [event["args"][:3] for event in self.events("timeout")]
        self.assertEqual(deadlines, [["--signal=TERM", "--kill-after=5s", "15s"],
                                     ["--signal=TERM", "--kill-after=5s", "20s"]])

    def test_setup_failures_use_fallback_without_retrying_ssh(self):
        for variables, expected in [({"SCAN_OUTPUT": ""}, "ssh_host_keys_unavailable"),
                                    ({"COMPUTE_KNOWN_HOSTS": "node ssh-rsa AAAAOther"}, "ssh_host_key_untrusted"),
                                    ({"PREFLIGHT_EXIT": "255", "PREFLIGHT_ERROR": "Permission denied"},
                                     "ssh_authentication_failed")]:
            with self.subTest(expected=expected):
                self.assert_setup(expected, **variables)
                count = len(self.events("ssh"))
                result = self.invoke(FAILURE)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("notification_submitted_fallback", result.stdout)
                self.assertIn("alertmanager_skipped_ssh_not_ready", result.stdout)
                self.assertEqual(len(self.events("ssh")), count)
                payload = json.loads(self.events("curl")[-1]["payload"])
                self.assertIn("could not establish customer deploy health", payload["text"])
                self.assertNotIn("a customer could not deploy", payload["text"])
                self.assertIn(f"Stage: Control-plane SSH preflight ({expected})", payload["text"])
                self.assertIn(f"First check: Open the failed SSH preflight step and follow the runbook for {expected}.", payload["text"])
                self.assertIn("Fleet: production-us", payload["text"])
                self.assertIn("Customer impact: unknown", payload["text"])
                self.assertIn("Last successful check: unknown", payload["text"])
                self.assertIn("Failed run: https://github.com/example/gregale/actions/runs/42", payload["text"])
                self.assertNotIn(SECRET, payload["text"])
                self.assertTrue(self.events("curl")[-1]["uses_private_config"])
                self.assertNotIn(SECRET, " ".join(self.events("curl")[-1]["args"]))
                self.assertNotIn(SECRET, result.stdout + result.stderr)

    def test_missing_setup_also_uses_independent_fallback(self):
        result = self.invoke(FAILURE, CP_HOST="", CP_SSH_KEY="")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(self.events("ssh"), [])
        self.assertEqual(len(self.events("curl")), 1)

    def test_successful_primary_submission_does_not_duplicate_the_page(self):
        self.assert_setup()
        result = self.invoke(FAILURE, CANARY_PROBE_OUTCOME="failure")
        self.assertEqual(result.returncode, 0)
        self.assertIn("notification_submitted_alertmanager", result.stdout)
        self.assertEqual(self.events("curl"), [])

    def test_failed_or_timed_out_primary_submission_reaches_fallback(self):
        self.assert_setup()
        for variables in ({"SSH_EXIT": "255"}, {"TIMEOUT_DURATION": "120s"}):
            with self.subTest(variables=variables):
                result = self.invoke(FAILURE, CANARY_PROBE_OUTCOME="failure", **variables)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("notification_submitted_fallback", result.stdout)
                self.assertNotIn(SECRET, result.stdout + result.stderr)
                args = self.events("curl")[-1]["args"]
                self.assertEqual(args[args.index("--max-time") + 1], "15")
                self.assertEqual(args[args.index("--connect-timeout") + 1], "3")
                self.assertEqual(args[args.index("--proto") + 1], "=https")
        primary = [event for event in self.events("timeout") if event["args"][2] == "120s"]
        self.assertEqual(len(primary), 2)

    def test_absent_fallback_is_a_loud_error_without_a_network_call(self):
        result = self.invoke(FAILURE, CANARY_FALLBACK_WEBHOOK_URL="")
        self.assertEqual(result.returncode, 1)
        self.assertIn("fallback_unconfigured", result.stderr)
        self.assertEqual(self.events("curl"), [])

    def test_manual_fallback_is_a_labeled_message_without_ssh(self):
        result = self.invoke(FAILURE, "--test-fallback", RUNNER_TEMP="")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("test_notification_submitted_fallback", result.stdout)
        self.assertEqual(self.events("ssh"), [])
        self.assertEqual(self.events("ssh-keyscan"), [])
        delivery, = self.events("curl")
        self.assertTrue(delivery["uses_private_config"])
        self.assertNotIn(SECRET, " ".join(delivery["args"]))
        payload = json.loads(delivery["payload"])
        self.assertIn("controlled canary fallback delivery test for production-us", payload["text"])
        self.assertIn("/actions/runs/42", payload["text"])

    def test_invalid_fallback_url_does_not_make_a_request(self):
        for url in ("http://chat.invalid", "https://chat.invalid/a b", 'https://chat.invalid/a"b', "https://chat.invalid/a\\b"):
            with self.subTest(url=url):
                result = self.invoke(FAILURE, "--test-fallback", CANARY_FALLBACK_WEBHOOK_URL=url)
                self.assertEqual(result.returncode, 1)
                self.assertIn("fallback_url_invalid", result.stderr)
                self.assertEqual(self.events("curl"), [])

    def test_fallback_transport_errors_and_non_2xx_are_not_submission_success(self):
        for settings in ([{"CURL_EXIT": "28"}] +
                         [{"HTTP_STATUS": status} for status in ("000", "301", "302", "307", "400", "500")]):
            with self.subTest(settings=settings):
                result = self.invoke(FAILURE, **settings)
                self.assertEqual(result.returncode, 1)
                self.assertIn("fallback_submission_failed", result.stderr)
                self.assertNotIn("notification_submitted_fallback", result.stdout)
                self.assertNotIn(SECRET, result.stdout + result.stderr)

    def test_remote_shell_preserves_hostile_log_text_without_executing_it(self):
        self.assert_setup()
        marker = self.directory / "must-not-exist"
        description = f"line one 'quote' \"double\"\n$(touch {marker}) `touch {marker}` \\backslash"
        (self.runner / "canary.log").write_text(description)
        result = self.invoke(FAILURE, EXECUTE_REMOTE="1", CANARY_PROBE_OUTCOME="failure")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(marker.exists())
        payload = json.loads(self.events("curl")[-1]["payload"])[0]
        self.assertEqual(payload["annotations"]["description"], description)
        self.assertEqual(payload["labels"]["severity"], "page")
        self.assertEqual(payload["annotations"]["run_url"], "https://github.com/example/gregale/actions/runs/42")
        self.assertEqual(payload["annotations"]["fleet"], "production-us")
        self.assertEqual(payload["annotations"]["active_release"], "a" * 40)
        self.assertEqual(payload["annotations"]["failure_stage"], "Deploy and invoke")
        self.assertIn("find the first failing step", payload["annotations"]["first_action"])

    def test_heartbeat_failure_does_not_claim_customer_deploy_failed(self):
        self.assert_setup()
        result = self.invoke(FAILURE, EXECUTE_REMOTE="1", CANARY_PROBE_OUTCOME="success",
                             CANARY_HEARTBEAT_OUTCOME="failure")
        self.assertEqual(result.returncode, 0, result.stderr)
        payload = json.loads(self.events("curl")[-1]["payload"])[0]
        self.assertIn("canary passed", payload["annotations"]["summary"])
        self.assertNotIn("a customer could not deploy", payload["annotations"]["summary"])
        self.assertEqual(payload["annotations"]["last_successful_check"], "Deploy and invoke passed in this run")
        self.assertEqual(payload["annotations"]["failure_stage"], "Independent heartbeat")


class WorkflowContractTest(unittest.TestCase):
    def test_failure_notification_remains_gated_and_bounded(self):
        workflow = (ROOT / ".github/workflows/synthetic-canary.yml").read_text()
        notification = workflow.split("- name: Page on canary failure\n")[1]
        self.assertIn("if: failure() && steps.delivery_configuration.outcome != 'failure' && (github.event_name == 'schedule' || inputs.page_on_failure)", notification)
        self.assertIn("timeout-minutes: 3", notification)
        self.assertIn('run: RUNNER_TEMP="${CANARY_WORK_DIR:-$RUNNER_TEMP}" bash scripts/ops/canary_failure.sh', notification)
        self.assertIn("if: github.event_name == 'schedule' || !inputs.test_fallback", workflow)
        self.assertIn("runs-on: [self-hosted, linux, faas-fleet-us]", workflow)
        self.assertIn("environment: production-us", workflow)
        self.assertIn('RUNNER_TEMP="$CANARY_WORK_DIR" bash scripts/ops/canary_ssh.sh setup', workflow)
        self.assertIn("RUN_ATTEMPT='${GITHUB_RUN_ATTEMPT}'", workflow)
        self.assertIn('rm -rf -- "$CANARY_WORK_DIR"', workflow)
        self.assertIn("if: github.event_name == 'workflow_dispatch' && inputs.test_fallback", workflow)
        self.assertIn("run: bash scripts/ops/canary_failure.sh --test-fallback", workflow)
        self.assertNotIn("$(ssh ", workflow)
        self.assertNotIn("~/.ssh/", workflow)
        for path in ("ci.yml", "ci-light.yml"):
            self.assertIn("make canary-transport-test", (ROOT / ".github/workflows" / path).read_text())


if __name__ == "__main__":
    unittest.main()
