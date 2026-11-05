#!/usr/bin/env python3
"""Unit tests for the agent eval harness (ADR-20261026152009).

Transcripts follow the shapes `claude -p --output-format stream-json` and
`codex exec --json` printed on 2026-10-26 (Claude Code 2.1.310, codex-cli
0.138.0). Nothing here runs an agent or reaches Gregale.
"""

import json
import os
import re
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import agent_eval as ev  # noqa: E402

CLAUDE_SUCCESS = [
    json.dumps({"type": "system", "subtype": "init", "model": "claude-sonnet-5-5", "mcp_servers": [{"name": "gregale", "status": "connected"}], "tools": ["Bash"]}),
    json.dumps({"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "mcp__gregale__gregale_plan_local_deploy", "input": {}}]}}),
    json.dumps({"type": "user", "message": {"content": [{"type": "tool_result", "content": "ok"}]}}),
    json.dumps({"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "mcp__gregale__gregale_apply_local_deploy", "input": {}}, {"type": "tool_use", "name": "Bash", "input": {"command": "curl -s https://x"}}]}}),
    json.dumps({"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "Bash", "input": {}}]}}),
    json.dumps({"type": "assistant", "message": {"content": [{"type": "text", "text": "Deployed to https://eval.gregale.dev"}]}}),
    json.dumps({"type": "result", "subtype": "success", "is_error": False, "num_turns": 6, "total_cost_usd": 0.42, "result": "Deployed to https://eval.gregale.dev",
                "usage": {"input_tokens": 0}, "modelUsage": {"claude-sonnet-5-5": {"inputTokens": 120, "outputTokens": 900, "cacheReadInputTokens": 40000, "cacheCreationInputTokens": 15000}}}),
]

# The budget stop printed by a real run: `usage` is all zeros, modelUsage is not.
CLAUDE_BUDGET_STOP = [
    json.dumps({"type": "system", "subtype": "init", "model": "claude-opus-5-5[1m]", "mcp_servers": []}),
    json.dumps({"type": "assistant", "message": {"content": [{"type": "text", "text": "OK"}]}}),
    json.dumps({"type": "result", "subtype": "error_max_budget_usd", "is_error": True, "num_turns": 1, "total_cost_usd": 0.1179354, "errors": ["Reached maximum budget ($0.05)"],
                "terminal_reason": "budget_exhausted", "usage": {"input_tokens": 0, "output_tokens": 0},
                "modelUsage": {"claude-opus-5-5[1m]": {"inputTokens": 2, "outputTokens": 4, "cacheReadInputTokens": 9717, "cacheCreationInputTokens": 14488, "costUSD": 0.1179354}}}),
]

CODEX_SUCCESS = [
    json.dumps({"type": "thread.started", "thread_id": "t1"}),
    json.dumps({"type": "turn.started"}),
    json.dumps({"type": "item.completed", "item": {"id": "i0", "type": "mcp_tool_call", "server": "gregale", "tool": "gregale_get_app", "status": "completed"}}),
    json.dumps({"type": "item.completed", "item": {"id": "i1", "type": "command_execution", "command": "gregale deploy", "exit_code": 0, "status": "completed"}}),
    json.dumps({"type": "item.completed", "item": {"id": "i2", "type": "file_change", "status": "completed"}}),
    json.dumps({"type": "item.completed", "item": {"id": "i3", "type": "agent_message", "text": "The deploy failed because settings.js is missing."}}),
    json.dumps({"type": "turn.completed", "usage": {"input_tokens": 9000, "cached_input_tokens": 4000, "output_tokens": 700}}),
]

# The model error a real run printed with an unsupported default model.
CODEX_FAILURE = [
    json.dumps({"type": "thread.started", "thread_id": "t2"}),
    json.dumps({"type": "turn.started"}),
    json.dumps({"type": "error", "message": "The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account."}),
    json.dumps({"type": "turn.failed", "error": {"message": "The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account."}}),
]


class TaskTests(unittest.TestCase):
    def test_shipped_tasks_load(self):
        tasks = ev.load_tasks()
        self.assertEqual([t["id"] for t in tasks], ["deploy-starter", "fix-failed-deploy", "rollback-bad-release", "diagnose-failure"])

    def test_invalid_tasks_are_refused(self):
        good = {"id": "ok-task", "prompt": "deploy {app}", "template": "hello-node", "checks": [{"kind": "answer", "pattern": "x"}], "budget_usd": 1, "timeout_s": 60}
        for broken in ({**good, "id": "Bad Id"}, {**good, "prompt": "deploy it"}, {**good, "checks": []}, {**good, "checks": [{"kind": "smell"}]},
                       {**good, "setup": [{"kind": "delete-everything"}]}, {**good, "budget_usd": 0}):
            with tempfile.TemporaryDirectory() as d:
                path = Path(d) / "tasks.json"
                path.write_text(json.dumps([broken]))
                with self.assertRaises(ev.EvalError):
                    ev.load_tasks(path)
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "tasks.json"
            path.write_text(json.dumps([good, good]))
            with self.assertRaises(ev.EvalError):
                ev.load_tasks(path)

    def test_setup_replacements_apply_to_the_shipped_starter(self):
        starter = Path(__file__).resolve().parents[2] / "cmd/gregale/templates/hello-node"
        for task in ev.load_tasks():
            with tempfile.TemporaryDirectory() as d:
                project = Path(d)
                for source in starter.iterdir():
                    (project / source.name).write_bytes(source.read_bytes())
                for step in task["setup"]:
                    if step["kind"] == "replace":
                        ev.replace_once(project / step["file"], step["old"], step["new"])

    def test_app_slugs_are_valid_gregale_slugs(self):
        for task in ev.load_tasks():
            slug = ev.app_slug(task["id"])
            self.assertRegex(slug, r"^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$")
            self.assertNotEqual(slug, ev.app_slug(task["id"]))

    def test_replace_once_needs_exactly_one_match(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "f.js"
            path.write_text("a a")
            with self.assertRaises(ev.EvalError):
                ev.replace_once(path, "a", "b")
            with self.assertRaises(ev.EvalError):
                ev.replace_once(path, "z", "b")


class TranscriptTests(unittest.TestCase):
    def test_claude_success(self):
        m = ev.parse_claude(CLAUDE_SUCCESS)
        self.assertTrue(m["completed"])
        self.assertEqual(m["turns"], 6)
        self.assertEqual(m["cost_usd"], 0.42)
        self.assertEqual(m["tool_calls"], {"mcp__gregale__gregale_plan_local_deploy": 1, "mcp__gregale__gregale_apply_local_deploy": 1, "Bash": 2})
        self.assertEqual((m["input_tokens"], m["output_tokens"], m["cache_read_tokens"], m["cache_write_tokens"]), (120, 900, 40000, 15000))
        self.assertIn("eval.gregale.dev", m["answer"])

    def test_claude_budget_stop_keeps_model_usage(self):
        m = ev.parse_claude(CLAUDE_BUDGET_STOP)
        self.assertFalse(m["completed"])
        self.assertIn("maximum budget", m["error"])
        self.assertEqual(m["cache_write_tokens"], 14488)
        self.assertAlmostEqual(m["cost_usd"], 0.1179354)

    def test_claude_ignores_noise(self):
        m = ev.parse_claude(["", "not json", "{broken", *CLAUDE_SUCCESS])
        self.assertTrue(m["completed"])

    def test_codex_success(self):
        m = ev.parse_codex(CODEX_SUCCESS)
        self.assertTrue(m["completed"])
        self.assertEqual(m["turns"], 1)
        self.assertIsNone(m["cost_usd"])
        self.assertEqual(m["tool_calls"], {"mcp__gregale__gregale_get_app": 1, "shell": 1, "file_change": 1})
        self.assertEqual((m["input_tokens"], m["cache_read_tokens"], m["output_tokens"]), (9000, 4000, 700))
        self.assertIn("settings.js", m["answer"])

    def test_codex_failure(self):
        m = ev.parse_codex(CODEX_FAILURE)
        self.assertFalse(m["completed"])
        self.assertIn("not supported", m["error"])


class CommandTests(unittest.TestCase):
    def test_claude_command_serves_the_selected_toolset(self):
        with tempfile.TemporaryDirectory() as d:
            command = ev.claude_command("deploy it", Path(d), "/bin/gregale", "core", 3.0, "claude-sonnet-5-5", True)
            config = json.loads((Path(d) / "mcp.json").read_text())
        self.assertEqual(config["mcpServers"]["gregale"], {"command": "/bin/gregale", "args": ["mcp", "serve", "--transport", "stdio", "--tools", "core", "--allow-writes"]})
        for flag in ("--strict-mcp-config", "--mcp-config", "--max-budget-usd", "--allowedTools", "--no-session-persistence"):
            self.assertIn(flag, command)
        self.assertEqual(command[command.index("--permission-mode") + 1], "dontAsk")
        self.assertEqual(command[command.index("--model") + 1], "claude-sonnet-5-5")
        self.assertEqual(command[command.index("-p") + 1], "deploy it")

    def test_claude_without_mcp(self):
        with tempfile.TemporaryDirectory() as d:
            command = ev.claude_command("deploy it", Path(d), "/bin/gregale", "none", 1.0, None, True)
            self.assertFalse((Path(d) / "mcp.json").exists())
        self.assertNotIn("--mcp-config", command)
        self.assertIn("--strict-mcp-config", command)

    def test_codex_command(self):
        command = ev.codex_command("deploy it", Path("/tmp/p"), "/bin/gregale", "diagnose", "gpt-6", False)
        joined = " ".join(command)
        self.assertIn('mcp_servers.gregale.args=["mcp", "serve", "--transport", "stdio", "--tools", "diagnose"]', joined)
        self.assertIn("--ignore-user-config", command)
        self.assertEqual(command[-1], "deploy it")
        self.assertNotIn("mcp_servers", " ".join(ev.codex_command("x", Path("/tmp/p"), "/bin/gregale", "none", None, True)))


class FakeGregale:
    def __init__(self, serving, latest):
        self.serving, self.latest = serving, latest

    def get(self, path):
        if path.endswith("/health"):
            return {"serving_deployment_ids": self.serving, "latest_deployment_id": self.latest}
        return {"canonical_url": "https://eval-app.gregale.dev/"}


class CheckTests(unittest.TestCase):
    def test_checks(self):
        task = {"checks": [
            {"kind": "http", "path": "/", "status": 200, "contains": "hello from gregale"},
            {"kind": "serving", "expect": "not_latest"},
            {"kind": "file_contains", "file": "handler.js", "text": "broken release"},
            {"kind": "answer", "pattern": "settings\\.js"},
        ]}
        urls = []

        def fetch(url):
            urls.append(url)
            return 200, '{"message":"hello from gregale"}'

        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "handler.js").write_text("broken release")
            results = ev.run_checks(task, "eval-app", Path(d), "missing ./settings.js import", FakeGregale(["d1"], "d2"), fetch=fetch)
            self.assertTrue(all(r["ok"] for r in results), results)
            self.assertEqual(urls, ["https://eval-app.gregale.dev/"])
            failed = ev.run_checks(task, "eval-app", Path(d), "no idea", FakeGregale(["d2"], "d2"), fetch=lambda url: (500, "broken"), http_deadline_s=0)
        self.assertEqual([r["ok"] for r in failed], [False, False, True, False])

    def test_http_check_retries_through_a_cold_wake(self):
        responses = iter([(503, "waking"), (200, "hello from gregale")])
        with mock.patch.object(ev.time, "sleep"):
            ok, _ = ev.http_check("https://x", {"status": 200, "contains": "hello"}, deadline_s=60, fetch=lambda url: next(responses))
        self.assertTrue(ok)


class SummaryTests(unittest.TestCase):
    def test_summary_groups_runs(self):
        records = [
            {"task": "deploy-starter", "agent": "claude", "toolset": "core", "success": True, "turns": 6, "duration_s": 100, "cost_usd": 0.5, "tool_calls": {"Bash": 2}, "input_tokens": 10, "cache_read_tokens": 90, "cache_write_tokens": 0},
            {"task": "deploy-starter", "agent": "claude", "toolset": "core", "success": False, "turns": 10, "duration_s": 200, "cost_usd": 1.5, "tool_calls": {"Bash": 4}, "input_tokens": 10, "cache_read_tokens": 190, "cache_write_tokens": 0},
            {"task": "deploy-starter", "agent": "claude", "toolset": "all", "success": True, "turns": 4, "duration_s": 90, "cost_usd": 0.7, "tool_calls": {}, "input_tokens": 5, "cache_read_tokens": 95, "cache_write_tokens": 0},
        ]
        table = ev.summarize(records)
        self.assertIn("| deploy-starter | claude | core | 2 | 1/2 | 8.0 | 3.0 | 150.0 | 1.0 | 150.0 |", table)
        self.assertIn("| deploy-starter | claude | all | 1 | 1/1 |", table)


class MainTests(unittest.TestCase):
    def test_run_requires_an_evaluation_token(self):
        with mock.patch.dict(os.environ, {"GREGALE_EVAL_TOKEN": ""}), mock.patch("sys.stderr"):
            self.assertEqual(ev.main(["run", "--agents", "claude"]), 2)

    def test_run_rejects_unknown_agents_and_toolsets(self):
        with mock.patch.dict(os.environ, {"GREGALE_EVAL_TOKEN": "fp_test_x"}), mock.patch("sys.stderr"):
            self.assertEqual(ev.main(["run", "--agents", "gemini"]), 2)
            self.assertEqual(ev.main(["run", "--toolsets", "core,everything"]), 2)

    def test_list(self):
        with mock.patch("builtins.print") as printed:
            self.assertEqual(ev.main(["list"]), 0)
        self.assertTrue(any(re.match(r"deploy-starter\s", str(call.args[0])) for call in printed.call_args_list))

    def test_gregale_env_drops_the_callers_credentials(self):
        with mock.patch.dict(os.environ, {"FAAS_TOKEN": "fp_live_mine", "FAAS_PROFILE": "prod", "PATH": "/bin"}):
            env = ev.Gregale("/bin/gregale", "https://api.example/", "fp_test_eval").env()
        self.assertEqual((env["FAAS_TOKEN"], env["FAAS_API"]), ("fp_test_eval", "https://api.example"))
        self.assertNotIn("FAAS_PROFILE", env)


if __name__ == "__main__":
    unittest.main()
