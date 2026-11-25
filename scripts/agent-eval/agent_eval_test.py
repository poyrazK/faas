#!/usr/bin/env python3
"""Unit tests for the agent eval harness (ADR-20261026152009).

Transcripts follow the shapes `claude -p --output-format stream-json` and
`codex exec --json` printed on 2026-10-26 (Claude Code 2.1.310, codex-cli
0.138.0). Nothing here runs an agent or reaches Gregale.
"""

import json
import os
import re
import subprocess
import sys
import tempfile
import types
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
        self.assertEqual([t["id"] for t in tasks], ["deploy-starter", "fix-failed-deploy", "rollback-bad-release", "diagnose-failure", "deploy-retail-store", "deploy-voting-app", "first-hour-private", "first-hour-edit",
                                                    "deploy-express-render", "migrate-fastapi-render", "deploy-flask-render", "migrate-django-heroku", "deploy-go-heroku",
                                                "day2-config-change", "day2-schedule", "day2-no-cold-start"])

    def test_framework_tasks_grade_the_fix_not_the_repo(self):
        # The pristine repositories leave these features unused; only the
        # change an agent has to make (pin uvicorn on $PORT, open Django's
        # ALLOWED_HOSTS beyond Heroku, replace the buildpack binary name)
        # counts as using them.
        tasks = {t["id"]: t for t in ev.load_tasks()}
        cases = [
            ("migrate-fastapi-render", "render.yaml", "    startCommand: uvicorn main:app --host 0.0.0.0 --port $PORT\n",
             "Procfile", "web: uvicorn main:app --host 0.0.0.0 --port $PORT\n", "start-command-pinned"),
            ("migrate-django-heroku", "gettingstarted/settings.py", 'ALLOWED_HOSTS = ["*"]\n',
             "gettingstarted/settings.py", 'ALLOWED_HOSTS = os.environ.get("DJANGO_ALLOWED_HOSTS", "").split(",")\n', "allowed-hosts-for-gregale"),
            # Baseline run 2026-11-17: the agent gated Heroku's production
            # block on its own flag instead of editing ALLOWED_HOSTS.
            ("migrate-django-heroku", "gettingstarted/settings.py",
             'IS_HEROKU_APP = "DYNO" in os.environ and "CI" not in os.environ\n\nif IS_HEROKU_APP:\n    # On Heroku\n    ALLOWED_HOSTS = ["*"]\n',
             "gettingstarted/settings.py",
             'IS_HEROKU_APP = "DYNO" in os.environ and "CI" not in os.environ\nIS_PRODUCTION = IS_HEROKU_APP or os.environ.get("ENVIRONMENT") == "production"\n\nif IS_PRODUCTION:\n    # On Heroku\n    ALLOWED_HOSTS = ["*"]\n',
             "allowed-hosts-for-gregale"),
            ("deploy-go-heroku", "Procfile", "web: go-getting-started\n",
             "gregale.yaml", "hosting:\n  start: ./out\n", "start-command-fixed"),
        ]
        for tid, pristine_file, pristine, fixed_file, fixed, feature in cases:
            with self.subTest(tid), tempfile.TemporaryDirectory() as tmp:
                project = Path(tmp)
                (project / pristine_file).parent.mkdir(parents=True, exist_ok=True)
                (project / pristine_file).write_text(pristine)
                self.assertNotIn(feature, ev.grade_features(tasks[tid], [], project, {})["features_used"])
                (project / fixed_file).parent.mkdir(parents=True, exist_ok=True)
                (project / fixed_file).write_text(fixed)
                self.assertIn(feature, ev.grade_features(tasks[tid], [], project, {})["features_used"])

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
            if task.get("repo"):
                continue
            with tempfile.TemporaryDirectory() as d:
                project = Path(d)
                for source in starter.iterdir():
                    (project / source.name).write_bytes(source.read_bytes())
                for step in task["setup"]:
                    if step["kind"] == "replace":
                        ev.replace_once(project / step["file"], step["old"], step["new"])

    def test_app_slugs_are_valid_gregale_slugs(self):
        for task in [t for t in ev.load_tasks() if not t.get("repo")]:
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
        self.assertIn("--ephemeral", command)

    def test_codex_repo_task_keeps_its_session_and_resumes_it(self):
        kept = ev.codex_command("deploy it", Path("/tmp/p"), "/bin/gregale", "none", None, False, keep_session=True)
        self.assertNotIn("--ephemeral", kept)
        self.assertEqual(kept[:3], ["codex", "exec", "--json"])
        resumed = ev.codex_command("Yes, go ahead.", Path("/tmp/p"), "/bin/gregale", "core", "gpt-6", False, resume="t1")
        self.assertEqual(resumed[:4], ["codex", "exec", "resume", "--json"])
        self.assertEqual(resumed[-2:], ["t1", "Yes, go ahead."])
        # `codex exec resume` has no -C/-s; the sandbox comes from config.
        self.assertNotIn("-s", resumed)
        self.assertNotIn("-C", resumed)
        joined = " ".join(resumed)
        for part in ('sandbox_mode="workspace-write"', "sandbox_workspace_write.network_access=true", "mcp_servers.gregale.command", "-m gpt-6"):
            self.assertIn(part, joined)
        self.assertEqual(ev.parse_codex(CODEX_SUCCESS)["session_id"], "t1")

    def test_follow_up_command_resumes_the_agents_own_session(self):
        options = mock.Mock(mcp_writes=False, codex_model=None, claude_model=None)
        task = {"follow_up": "Yes, go ahead.", "budget_usd": 4.0}
        g = ev.CliGregale("/bin/gregale")
        codex = ev.follow_up_command("codex", task, Path("/tmp/r"), Path("/tmp/r/project"), g, "none", options, "t1")
        self.assertEqual(codex[:3], ["codex", "exec", "resume"])
        claude = ev.follow_up_command("claude", task, Path("/tmp/r"), Path("/tmp/r/project"), g, "none", options, "s1")
        self.assertEqual(claude[0], "claude")
        self.assertEqual(claude[claude.index("--resume") + 1], "s1")
        self.assertIn("codex", ev.FOLLOW_UP_AGENTS)


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

    def test_private_app_is_graded_on_its_serving_deployment(self):
        # Apps are private by default: a public GET gets 401, so the grader asks
        # the serving deployment (not the latest, a bad release after a rollback).
        class PrivateGregale(FakeGregale):
            verified = []

            def cli(self, *args, **kwargs):
                self.verified.append(args[2])
                body = "hello from gregale" if args[2] == "d1" else "broken"
                return subprocess.CompletedProcess(args, 0, json.dumps({"http_status": 200, "body": body}), "")

        task = {"checks": [{"kind": "http", "path": "/", "status": 200, "contains": "hello from gregale"}]}
        g = PrivateGregale(["d1"], "d2")
        with mock.patch.object(ev, "_fetch", return_value=(401, "this app requires an API key")), \
                tempfile.TemporaryDirectory() as d:
            results = ev.run_checks(task, "eval-app", Path(d), "", g, http_deadline_s=0)
        self.assertTrue(results[0]["ok"], results)
        self.assertEqual(g.verified, ["d1"])

    def test_vote_counted_casts_a_vote_and_reads_it_back(self):
        # rc.380: both pages answered 200 while every vote POST returned 500
        # and the votes table never existed; the grader passed it.
        class VotingApps:
            def __init__(self, post_status=200, counts=True, scores=None, moved=0, result_status=200):
                self.scores = scores if scores is not None else {"a": 3, "b": 1}
                self.post_status, self.counts, self.moved, self.result_status = post_status, counts, moved, result_status
                self.calls, self.sessions = [], 0

            def __call__(self, method, url, data=None, headers=None, timeout=30):
                self.calls.append((method, url, data, (headers or {}).get("Cookie", "")))
                if url.startswith("https://vote.example/"):
                    if method == "POST" and self.post_status == 200 and self.counts:
                        option = data.decode().split("=")[1]
                        self.scores[option] = self.scores.get(option, 0) + 1
                    return self.post_status, "<html>" if self.post_status == 200 else "Internal Server Error"
                if self.result_status != 200:
                    return self.result_status, "Unauthorized"
                if url == "https://result.example/socket.io/?EIO=4&transport=polling":
                    self.sessions += 1
                    return 200, '0{"sid":"s/%d","pingInterval":25000}' % self.sessions
                if self.moved:
                    self.moved -= 1
                    return 400, '{"code":1,"message":"Session ID unknown"}'
                if method == "POST":
                    return 200, "ok"
                payload = json.dumps(json.dumps(self.scores)) if isinstance(self.scores, dict) else json.dumps(self.scores)
                return 200, '40{"sid":"x"}\x1e2\x1e42["message",{"text":"Welcome!"}]\x1e42["scores",' + payload + "]"

        class Urls:
            def get(self, path):
                return {"/v1/apps/vote": {"canonical_url": "https://vote.example"},
                        "/v1/apps/vote-result": {"canonical_url": "https://result.example/"},
                        "/v1/apps/vote-worker": {}}[path]

        task = {"checks": [{"kind": "vote_counted"}]}
        # Every slug carries the project's "vote" prefix: names are anchored,
        # so the result app is never taken for the vote app.
        created = {"apps": ["vote-result", "vote-worker", "vote"]}

        def grade(apps, created=created):
            (result,) = ev.run_checks(task, None, Path("."), "", Urls(), created=created, http=apps, http_deadline_s=0)
            return result

        apps = VotingApps()
        self.assertEqual(grade(apps), {"kind": "vote_counted", "ok": True, "detail": "b: 1 -> 2"})
        post = [c for c in apps.calls if c[0] == "POST" and c[1].startswith("https://vote.example")]
        self.assertEqual(post[0][2], b"vote=b", "votes for the option with fewer votes")
        self.assertRegex(post[0][3], r"^voter_id=agent-eval-[0-9a-f]{16}$")
        closes = [c for c in apps.calls if c[2] == b"41\x1e1"]
        self.assertEqual(len(closes), apps.sessions, "every Socket.IO session is closed")
        self.assertIn(("POST", "https://result.example/socket.io/?EIO=4&transport=polling&sid=s/1", b"3", ""), apps.calls)

        moved = VotingApps(moved=1)
        self.assertTrue(grade(moved)["ok"], "a session that lands on another instance starts again")
        self.assertEqual(grade(VotingApps(post_status=500))["detail"][:22], "vote vote POST 500: 'I")
        self.assertIn("requires sign-in", grade(VotingApps(post_status=401))["detail"])
        self.assertIn("requires sign-in", grade(VotingApps(result_status=401))["detail"])
        self.assertEqual(grade(VotingApps(counts=False))["detail"], "vote accepted but not counted: b stayed 1")
        malformed = grade(VotingApps(scores=[1, 2]))
        self.assertFalse(malformed["ok"])
        self.assertIn("not a count per option", malformed["detail"])
        self.assertFalse(grade(VotingApps(), created={"apps": ["vote", "vote-worker"]})["ok"])

        class SuffixedUrls(Urls):
            def get(self, path):
                return {"/v1/apps/vote-k3x7q": {"canonical_url": "https://vote.example"},
                        "/v1/apps/result-a2b4c": {"canonical_url": "https://result.example"}}[path]
        (suffixed,) = ev.run_checks(task, None, Path("."), "", SuffixedUrls(), created={"apps": ["result-a2b4c", "vote-k3x7q"]},
                                    http=VotingApps(), http_deadline_s=0)
        self.assertTrue(suffixed["ok"], "a name another account holds gets a 5-character suffix")

    def test_day2_checks_read_the_app_record_and_cli(self):
        class Day2:
            def __init__(self, app, cron_out="", code=0):
                self.app, self.cron_out, self.code, self.calls = app, cron_out, code, []

            def get(self, path):
                return self.app

            def cli(self, *args, **kwargs):
                self.calls.append(args)
                return mock.Mock(returncode=self.code, stdout=self.cron_out, stderr="")

        task = {"checks": [{"kind": "app_field", "field": "min_instances", "min": 1},
                           {"kind": "cli_output", "args": ["cron", "list", "--app", "{app}"], "patterns": ["/tick", "(\\*/15|15m)"]}]}
        cron = '{"items":[{"path":"/tick","method":"POST","schedule":"*/15 * * * *"}]}'
        g = Day2({"min_instances": 1}, cron)
        warm, scheduled = ev.run_checks(task, "eval-app", Path("."), "", g)
        self.assertEqual((warm["ok"], warm["detail"]), (True, "min_instances=1"))
        self.assertEqual((scheduled["ok"], scheduled["detail"]), (True, "all patterns present"))
        self.assertEqual(g.calls, [("cron", "list", "--app", "eval-app")])

        cold, hourly = ev.run_checks(task, "eval-app", Path("."), "", Day2({"min_instances": 0}, cron.replace("*/15", "0")))
        self.assertFalse(cold["ok"])
        self.assertFalse(hourly["ok"])
        self.assertIn("missing", hourly["detail"])
        _, failed = ev.run_checks(task, "eval-app", Path("."), "", Day2({}, "boom", code=4))
        self.assertIn("exited 4", failed["detail"])
        self.assertFalse(ev.app_field_check({"min_instances": None}, {"field": "min_instances", "min": 1})[0])
        self.assertTrue(ev.app_field_check({"require_authn": True}, {"field": "require_authn", "equals": True})[0])

    def test_live_worker_counts_as_serving(self):
        # Health reads "unsupported" for a worker pool unless app_health_workers
        # is on; a live worker serves its queue (2026-11-24 voting-app rerun).
        class WorkerGregale:
            def __init__(self, availability):
                self.availability = availability

            def get(self, path):
                if path.endswith("/health"):
                    return {"phase": "unsupported", "serving_deployment_ids": []}
                return {"workload_class": "worker", "deployment_availability": self.availability}

        task = {"checks": [{"kind": "apps_serving"}]}
        with tempfile.TemporaryDirectory() as d:
            live = ev.run_checks(task, "worker", Path(d), "", WorkerGregale("live"), created={"apps": ["worker"]})
            down = ev.run_checks(task, "worker", Path(d), "", WorkerGregale("unavailable"), created={"apps": ["worker"]})
        self.assertTrue(live[0]["ok"], live)
        self.assertFalse(down[0]["ok"], down)

    def test_command_feature_counts_its_mcp_tool(self):
        task = {"features": [{"id": "verify", "kind": "command", "pattern": r"\brelease\s+verify\b",
                              "tools": ["gregale_verify_application_response"]}]}
        with tempfile.TemporaryDirectory() as d:
            mcp = ev.grade_features(task, ["gregale deploy"], Path(d), {}, tools=["Bash", "mcp__gregale__gregale_verify_application_response"])
            none = ev.grade_features(task, ["gregale deploy"], Path(d), {}, tools=["Bash"])
        self.assertEqual(mcp["features_used"], ["verify"])
        self.assertEqual(none["features_missed"], ["verify"])

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
            self.assertEqual(ev.main(["run", "--agents", "aider"]), 2)
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


# Shapes from the Gemini CLI 0.45.2 and cursor-agent 2026.04.17 stream-json
# writers (read from their bundles on 2026-11-12; neither could log in here).
GEMINI_SUCCESS = [
    json.dumps({"type": "init", "session_id": "s", "model": "gemini-3-pro"}),
    json.dumps({"type": "message", "role": "user", "content": "Deploy it"}),
    json.dumps({"type": "message", "role": "assistant", "content": "Let me look.", "delta": True}),
    json.dumps({"type": "tool_use", "tool_name": "run_shell_command", "tool_id": "t1", "parameters": {"command": "gregale deploy --dry-run"}}),
    json.dumps({"type": "tool_result", "tool_id": "t1", "status": "success", "output": "ok"}),
    json.dumps({"type": "tool_use", "tool_name": "read_file", "tool_id": "t2", "parameters": {"path": "README.md"}}),
    json.dumps({"type": "message", "role": "assistant", "content": "Deployed to ", "delta": True}),
    json.dumps({"type": "message", "role": "assistant", "content": "https://ui.gregale.dev", "delta": True}),
    json.dumps({"type": "result", "status": "success", "stats": {"total_tokens": 1500, "input_tokens": 1200, "output_tokens": 300, "cached": 1000, "duration_ms": 9000, "tool_calls": 2}}),
]

CURSOR_SUCCESS = [
    json.dumps({"type": "system", "subtype": "init", "session_id": "s"}),
    json.dumps({"type": "assistant", "message": {"content": [{"type": "text", "text": "Checking."}]}, "model_call_id": "m1"}),
    json.dumps({"type": "tool_call", "subtype": "started", "call_id": "c1", "model_call_id": "m1", "tool_call": {"shellToolCall": {"args": {"command": "curl -s https://api.gregale.dev/v1/apps"}}}}),
    json.dumps({"type": "tool_call", "subtype": "completed", "call_id": "c1", "model_call_id": "m1", "tool_call": {"shellToolCall": {"args": {"command": "curl -s https://api.gregale.dev/v1/apps"}, "result": {}}}}),
    json.dumps({"type": "tool_call", "subtype": "started", "call_id": "c2", "model_call_id": "m2", "tool_call": {"readToolCall": {"args": {"path": "a"}}}}),
    json.dumps({"type": "result", "subtype": "success", "is_error": False, "duration_ms": 5000, "result": "Live at https://ui.gregale.dev",
                "usage": {"inputTokens": 800, "outputTokens": 90, "cacheReadTokens": 4000, "cacheWriteTokens": 100}}),
]


class NewAgentTests(unittest.TestCase):
    def test_gemini_transcript(self):
        m = ev.parse_gemini(GEMINI_SUCCESS)
        self.assertTrue(m["completed"])
        self.assertEqual(m["commands"], ["gregale deploy --dry-run"])
        self.assertEqual(m["tool_calls"], {"run_shell_command": 1, "read_file": 1})
        self.assertEqual(m["answer"], "Deployed to https://ui.gregale.dev")
        self.assertEqual((m["input_tokens"], m["cache_read_tokens"], m["output_tokens"], m["turns"]), (200, 1000, 300, 3))
        failed = ev.parse_gemini([json.dumps({"type": "result", "status": "error", "error": {"type": "auth", "message": "IneligibleTierError"}})])
        self.assertFalse(failed["completed"])
        self.assertIn("IneligibleTierError", failed["error"])

    def test_cursor_transcript(self):
        m = ev.parse_cursor(CURSOR_SUCCESS)
        self.assertTrue(m["completed"])
        self.assertEqual(m["commands"], ["curl -s https://api.gregale.dev/v1/apps"])
        self.assertEqual(m["tool_calls"], {"shellToolCall": 1, "readToolCall": 1})
        self.assertEqual(m["answer"], "Live at https://ui.gregale.dev")
        self.assertEqual((m["input_tokens"], m["cache_read_tokens"], m["cache_write_tokens"], m["turns"]), (800, 4000, 100, 2))

    def test_claude_and_codex_record_shell_commands(self):
        self.assertEqual(ev.parse_claude(CLAUDE_SUCCESS)["commands"], ["curl -s https://x"])
        self.assertEqual(ev.parse_codex(CODEX_SUCCESS)["commands"], ["gregale deploy"])

    def test_gemini_command_points_settings_at_the_run(self):
        with tempfile.TemporaryDirectory() as d:
            command, env = ev.gemini_command("deploy it", Path(d), "/bin/gregale", "core", "gemini-3-pro", False)
            settings = json.loads(Path(env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"]).read_text())
            self.assertEqual(settings["mcpServers"]["gregale"]["args"], ["mcp", "serve", "--transport", "stdio", "--tools", "core"])
            bare, bare_env = ev.gemini_command("deploy it", Path(d), "/bin/gregale", "none", None, False)
        self.assertEqual(command[command.index("--approval-mode") + 1], "yolo")
        self.assertIn("--skip-trust", command)
        self.assertEqual(bare_env, {})
        self.assertNotIn("--allowed-mcp-server-names", bare)

    def test_cursor_command_writes_workspace_mcp(self):
        with tempfile.TemporaryDirectory() as d:
            command = ev.cursor_command("deploy it", Path(d), "/bin/gregale", "all", None, True)
            config = json.loads((Path(d) / ".cursor" / "mcp.json").read_text())
            bare = ev.cursor_command("deploy it", Path(d) / "x", "/bin/gregale", "none", None, True)
            self.assertFalse((Path(d) / "x" / ".cursor").exists())
        self.assertEqual(config["mcpServers"]["gregale"]["args"][-1], "--allow-writes")
        for flag in ("--force", "--trust", "--approve-mcps"):
            self.assertIn(flag, command)
        self.assertNotIn("--approve-mcps", bare)

    def test_claude_repo_tasks_may_run_any_shell_command(self):
        with tempfile.TemporaryDirectory() as d:
            command = ev.claude_command("x", Path(d), "/bin/gregale", "none", 1.0, None, True, any_shell=True)
        self.assertIn("Bash", command)
        self.assertNotIn("Bash(gregale *)", command)

    def test_gemini_needs_an_api_key(self):
        with mock.patch.dict(os.environ, {"GEMINI_API_KEY": "", "GOOGLE_API_KEY": "", "GOOGLE_GENAI_USE_VERTEXAI": ""}):
            self.assertIn("GEMINI_API_KEY", ev.agent_ready("gemini"))
        with mock.patch.dict(os.environ, {"GEMINI_API_KEY": "k"}):
            self.assertEqual(ev.agent_ready("gemini"), "")

    def test_claude_repo_runs_keep_their_session_and_can_resume(self):
        with tempfile.TemporaryDirectory() as d:
            starter = ev.claude_command("x", Path(d), "/bin/gregale", "none", 1.0, None, True)
            repo = ev.claude_command("x", Path(d), "/bin/gregale", "none", 1.0, None, True, any_shell=True, resume="sid-1")
        self.assertIn("--no-session-persistence", starter)
        self.assertNotIn("--no-session-persistence", repo)
        self.assertEqual(repo[repo.index("--resume") + 1], "sid-1")
        self.assertIn("WebFetch", repo)
        self.assertEqual(ev.parse_claude(CLAUDE_SUCCESS[:1] + [json.dumps({"type": "result", "subtype": "success", "session_id": "sid-9"})])["session_id"], "sid-9")

    def test_follow_up_metrics_merge(self):
        first = {"turns": 3, "cost_usd": 0.5, "input_tokens": 10, "output_tokens": 1, "cache_read_tokens": 100, "cache_write_tokens": 0,
                 "tool_calls": {"Bash": 2}, "commands": ["gregale deploy --dry-run"], "answer": "Shall I go ahead?", "completed": True}
        second = {"turns": 5, "cost_usd": 1.0, "input_tokens": 20, "output_tokens": 2, "cache_read_tokens": 300, "cache_write_tokens": 5,
                  "tool_calls": {"Bash": 3, "Read": 1}, "commands": ["gregale deploy --project"], "answer": "Live.", "completed": True}
        merged = ev.merge_metrics(first, second)
        self.assertEqual((merged["turns"], merged["cost_usd"], merged["cache_read_tokens"]), (8, 1.5, 400))
        self.assertEqual(merged["tool_calls"], {"Bash": 5, "Read": 1})
        self.assertEqual(merged["commands"], ["gregale deploy --dry-run", "gregale deploy --project"])
        self.assertEqual((merged["answer"], merged["first_answer"]), ("Live.", "Shall I go ahead?"))

    def test_stale_cursor_login_is_not_ready(self):
        stale = mock.Mock(returncode=0, stdout="✓ Login successful!\nLogged in (unable to fetch user details)\n", stderr="")
        with mock.patch.dict(os.environ, {"CURSOR_API_KEY": ""}), mock.patch.object(ev.subprocess, "run", return_value=stale):
            self.assertIn("cursor-agent login", ev.agent_ready("cursor"))

    def test_agent_markers_do_not_leak_into_the_agent(self):
        with mock.patch.dict(os.environ, {"CLAUDECODE": "1", "GEMINI_CLI": "1", "PATH": "/bin"}):
            env = ev.Gregale("/bin/gregale", "https://api.example", "t").env()
        self.assertNotIn("CLAUDECODE", env)
        self.assertNotIn("GEMINI_CLI", env)


class RepoTaskTests(unittest.TestCase):
    def test_repo_task_validation(self):
        good = {"id": "repo-task", "prompt": "Deploy {repo_url}", "repo": {"url": "https://github.com/o/r", "commit": "a" * 40},
                "checks": [{"kind": "apps_created"}], "features": [{"id": "dry-run", "kind": "command", "pattern": "--dry-run"}], "budget_usd": 1, "timeout_s": 60}
        for broken in ({**good, "repo": {"url": "https://example.com/o/r", "commit": "a" * 40}}, {**good, "repo": {"url": "https://github.com/o/r", "commit": "main"}},
                       {**good, "template": "hello-node"}, {**good, "setup": [{"kind": "deploy"}]},
                       {**good, "features": [{"id": "x1", "kind": "smell"}]}, {**good, "features": [{"id": "bad-re", "kind": "command", "pattern": "("}]},
                       {**good, "features": [{"id": "st", "kind": "state", "state": "moon"}]}):
            with tempfile.TemporaryDirectory() as d:
                path = Path(d) / "tasks.json"
                path.write_text(json.dumps([broken]))
                with self.assertRaises(ev.EvalError):
                    ev.load_tasks(path)
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "tasks.json"
            path.write_text(json.dumps([good]))
            self.assertEqual(len(ev.load_tasks(path)), 1)

    def test_repo_prompt_is_the_owners_words(self):
        task = {"prompt": "Deploy {repo_url} please", "repo": {"url": "https://github.com/o/r", "commit": "a" * 40}}
        self.assertEqual(ev.task_prompt(task, None, "core"), "Deploy https://github.com/o/r please")
        self.assertIn("logged in", ev.task_prompt({"prompt": "deploy {app}"}, "eval-x", "none"))

    def test_created_since(self):
        before = {"apps": {"old"}, "projects": set()}
        after = {"apps": {"old", "ui", "cart"}, "projects": {"shop"}}
        self.assertEqual(ev.created_since(before, after), {"apps": ["cart", "ui"], "projects": ["shop"], "databases": []})


class ProjectGregale:
    """Two created apps: ui (public, serving, with a park older than the run)
    and orders (internal, parked during the run, one failed deployment)."""

    def get(self, path):
        if path == "/v1/apps/ui":
            return {"canonical_url": "https://ui.gregale.dev/", "parked_deployment": {"parked_reason": "liveness_exhausted", "parked_at": "2026-11-12T16:59:17Z"}}
        if path == "/v1/apps/orders":
            return {"canonical_url": "", "status": "evicted_cold", "parked_deployment": {"parked_reason": "liveness_exhausted", "parked_at": "2026-11-12T20:10:00Z"}}
        if path == "/v1/apps/ui/health":
            return {"serving_deployment_ids": ["d1"]}
        if path == "/v1/apps/orders/health":
            return {"serving_deployment_ids": []}
        if path.startswith("/v1/apps/orders/deployments"):
            return {"items": [{"created_at": "2026-11-12T20:05:00Z", "status": "failed", "error_code": "app_startup_timeout"},
                              {"created_at": "2026-11-11T10:00:00Z", "status": "failed", "error_code": "image_not_found"}]}
        if path.startswith("/v1/apps/ui/deployments"):
            return {"items": [{"created_at": "2026-11-12T16:00:00Z", "status": "failed", "error_code": "app_startup_timeout"}]}
        return []


class ProjectCheckTests(unittest.TestCase):
    def test_project_checks(self):
        task = {"checks": [{"kind": "apps_created", "min": 2}, {"kind": "apps_serving"}, {"kind": "app_http", "match": "ui", "path": "/catalog", "status": 200},
                           {"kind": "app_http", "match": "checkout", "status": 200}, {"kind": "no_traps"}]}
        urls = []

        def fetch(url):
            urls.append(url)
            return 200, "<html>Retail Store</html>"

        with tempfile.TemporaryDirectory() as d:
            results = ev.run_checks(task, None, Path(d), "", ProjectGregale(), fetch=fetch, created={"apps": ["orders", "ui"], "projects": ["shop"]},
                                   since="2026-11-12T20:00:00Z")
        self.assertEqual([r["ok"] for r in results], [True, False, True, False, False])
        self.assertEqual(urls, ["https://ui.gregale.dev/catalog"])
        self.assertEqual(results[1]["detail"], "not serving: orders")
        # ui's park and failed deployment predate the run, so only orders counts.
        self.assertEqual(results[4]["detail"], "orders:app_startup_timeout,orders:liveness_exhausted")


class CliLoginTests(unittest.TestCase):
    def test_reads_and_touched_apps_go_through_the_cli(self):
        outputs = {
            ("app", "list"): '{"slug":"old","id":"i-old"}\n{"slug":"retail-ui","id":"i-ui"}\n{"slug":"new","id":"i-new"}\n',
            ("project", "list"): '{"slug":"shop"}\n',
            ("postgres", "list"): '{"items":[{"name":"old-db"},{"name":"new-db"}]}\n',
            ("release", "list", "--all", "--limit", "200"): '{"app_id":"i-ui","created_at":"2026-11-12T20:00:05Z"}\n{"app_id":"i-old","created_at":"2026-11-12T19:00:00Z"}\n',
            ("app", "health", "retail-ui"): '{\n  "serving_deployment_ids": ["d1"]\n}\n',
        }
        calls = []

        def cli(*args, **kwargs):
            calls.append(args)
            return mock.Mock(returncode=0, stdout=outputs[args], stderr="")

        g = ev.CliGregale("/bin/gregale")
        with mock.patch.object(g, "cli", side_effect=cli):
            self.assertEqual(g.get("/v1/apps/retail-ui/health"), {"serving_deployment_ids": ["d1"]})
            before = {"apps": {"old", "retail-ui"}, "projects": {"shop"}, "databases": {"old-db"}}
            touched = g.touched_since(before, "2026-11-12T20:00:00Z")
        self.assertEqual(touched, {"apps": ["new", "retail-ui"], "projects": [], "databases": ["new-db"]})
        with self.assertRaises(ValueError):
            g.get("/v1/billing")

    def test_cli_login_keeps_the_callers_profile(self):
        with mock.patch.dict(os.environ, {"FAAS_PROFILE": "default", "CLAUDECODE": "1"}):
            env = ev.CliGregale("/bin/gregale").env()
        self.assertEqual(env.get("FAAS_PROFILE"), "default")
        self.assertNotIn("CLAUDECODE", env)
        self.assertNotIn("FAAS_TOKEN", env)

    def test_cli_login_deletes_only_what_the_run_created(self):
        g = ev.CliGregale("/bin/gregale")
        before = {"apps": {"retail-ui", "eval-x-old"}, "projects": {"shop"}}
        # rb-java-off is another session's app, created while the run was going (2026-11-15).
        after = {"apps": {"retail-ui", "eval-x-old", "voting-redis", "eval-x-1", "rb-java-off"}, "projects": {"shop", "voting", "h15"}}
        touched = {"apps": ["retail-ui", "voting-redis"], "projects": ["voting"]}
        evidence = "gregale deploy --image redis --name voting-redis\ngregale deploy --project --project-slug voting"
        with mock.patch.object(g, "inventory", return_value=after):
            self.assertEqual(ev.deletable(g, before, touched, "eval-x-1", evidence),
                             ({"apps": ["eval-x-1", "voting-redis"], "projects": ["voting"], "databases": []}, "eval-x-1"))
            # A slug that existed before the run (or never got created) is not deleted.
            self.assertEqual(ev.deletable(g, before, touched, "eval-x-old", evidence)[1], None)
            # With no evidence only the harness's own slug is the run's.
            self.assertEqual(ev.deletable(g, before, touched, "eval-x-1"), ({"apps": ["eval-x-1"], "projects": [], "databases": []}, "eval-x-1"))
        # Without an inventory from before the run nothing is provably the run's.
        self.assertEqual(ev.deletable(g, None, touched, "eval-x-1"), ({"apps": [], "projects": [], "databases": []}, None))
        # A dedicated eval account deletes everything the run touched.
        plain = ev.Gregale("/bin/gregale", "https://api.example", "tok")
        self.assertEqual(ev.deletable(plain, before, touched, "eval-x-1"), (touched, "eval-x-1"))

    def test_only_apps_the_agent_named_are_the_runs(self):
        g = ev.CliGregale("/bin/gregale")
        with tempfile.TemporaryDirectory() as d:
            project = Path(d)
            (project / "docker-compose.yml").write_text("services:\n  vote:\n    build: ./vote\n  result:\n    build: ./result\n")
            (project / "node_modules").mkdir()
            (project / "node_modules" / "pkg.json").write_text('{"name": "zq-imm-repro-1115"}')
            metrics = {"commands": ["gregale deploy --name voting-app-redis --yes"],
                       "tool_inputs": ['{"app": "voting-worker"}']}
            evidence = ev.run_evidence(metrics, project)
        touched = {"apps": ["result", "vote", "voting-app-redis", "voting-worker", "zq-imm-repro-1115", "rb-java-off", "votes"],
                   "projects": ["h15"]}
        mine, others = ev.attribute(g, touched, evidence)
        self.assertEqual(mine, {"apps": ["result", "vote", "voting-app-redis", "voting-worker"], "projects": [], "databases": []})
        self.assertEqual(others, {"apps": ["zq-imm-repro-1115", "rb-java-off", "votes"], "projects": ["h15"], "databases": []})
        # A dedicated eval account has only the run in it.
        plain = ev.Gregale("/bin/gregale", "https://api.example", "tok")
        self.assertEqual(ev.attribute(plain, touched, ""), (touched, {"apps": [], "projects": [], "databases": []}))

    def test_cleanup_purges_deleted_apps_so_the_next_run_gets_the_name(self):
        calls = []
        failing = {("app", "delete", "--yes", "--delete-bindings", "stuck"), ("app", "purge", "--yes", "held")}

        def cli(*args, **kwargs):
            calls.append(args)
            return mock.Mock(returncode=1 if args in failing else 0, stdout="", stderr="")

        g = ev.CliGregale("/bin/gregale")
        with mock.patch.object(g, "cli", side_effect=cli):
            left = ev.cleanup(g, {"apps": ["express-hello-world", "held", "stuck"], "projects": ["voting"]}, None)
        self.assertEqual(calls, [
            ("project", "delete", "--yes", "--detach-services", "voting"),
            ("app", "delete", "--yes", "--delete-bindings", "express-hello-world"),
            ("app", "purge", "--yes", "express-hello-world"),
            ("app", "delete", "--yes", "--delete-bindings", "held"),
            ("app", "purge", "--yes", "held"),
            # An app that could not be deleted is not purged.
            ("app", "delete", "--yes", "--delete-bindings", "stuck"),
        ])
        self.assertEqual(left, ["name:held", "app:stuck"])

    def test_project_members_belong_to_the_run(self):
        # rc.381 retail-store: Compose named retail-catalog and retail-carts
        # from its services; the agent never typed them, so they went
        # unattributed, failed apps_created and were left behind.
        g = ev.CliGregale("/bin/gregale")
        lists = {"/v1/projects": {"items": [{"id": "p1", "slug": "retail-store"}, {"id": "p2", "slug": "other"}]},
                 "/v1/apps": {"items": [{"slug": "retail-catalog", "project_id": "p1"}, {"slug": "retail-carts", "project_id": "p1"},
                                        {"slug": "bh57-py", "project_id": ""}, {"slug": "side", "project_id": "p2"}]}}
        touched = {"apps": ["bh57-py", "retail-carts", "retail-catalog", "retail-ui", "side"], "projects": ["retail-store"], "databases": []}
        with mock.patch.object(g, "get", side_effect=lambda path: lists[path]):
            mine, others = ev.attribute(g, touched, "gregale deploy --project retail-store\ncurl https://retail-ui.gregale.app")
        self.assertEqual(mine["apps"], ["retail-carts", "retail-catalog", "retail-ui"])
        self.assertEqual(others["apps"], ["bh57-py", "side"])

    def test_cleanup_deletes_the_runs_databases_only(self):
        # The 2026-11-24 voting-app rerun found the account at its database
        # limit, filled by earlier runs' databases cleanup never deleted.
        g = ev.CliGregale("/bin/gregale")
        touched = {"apps": [], "projects": [], "databases": ["example-voting-app-db", "windmill-db"]}
        mine, others = ev.attribute(g, touched, "gregale postgres create example-voting-app-db")
        self.assertEqual(mine["databases"], ["example-voting-app-db"])
        self.assertEqual(others["databases"], ["windmill-db"])
        calls = []
        with mock.patch.object(g, "cli", side_effect=lambda *a, **k: calls.append(a) or mock.Mock(returncode=0)):
            left = ev.cleanup(g, mine, None)
        self.assertEqual(calls, [("postgres", "delete", "--yes", "example-voting-app-db")])
        self.assertEqual(left, [])

    def test_claude_tool_inputs_are_evidence_but_not_recorded(self):
        lines = [json.dumps({"type": "assistant", "message": {"content": [
            {"type": "tool_use", "name": "mcp__gregale__gregale_get_app", "input": {"app": "voting-app-redis"}}]}})]
        metrics = ev.parse_claude(lines)
        self.assertEqual(metrics["tool_inputs"], ['{"app": "voting-app-redis"}'])

    def test_cli_login_no_longer_implies_keep_apps(self):
        parsed = []

        def run_one(task, agent, toolset, gregale, options):
            parsed.append(options.keep_apps)
            return {"task": task["id"], "agent": agent, "toolset": toolset}

        with tempfile.TemporaryDirectory() as d, \
                mock.patch.object(ev, "run_one", side_effect=run_one), \
                mock.patch.object(ev, "agent_ready", return_value=""), \
                mock.patch.object(ev.shutil, "which", return_value="/bin/x"), \
                mock.patch("sys.stderr"), mock.patch("sys.stdout"):
            out = str(Path(d) / "r.jsonl")
            first = ev.load_tasks()[0]["id"]
            ev.main(["run", "--cli-login", "--toolsets", "core", "--tasks", first, "--out", out])
            ev.main(["run", "--cli-login", "--keep-apps", "--toolsets", "core", "--tasks", first, "--out", out])
        self.assertEqual(parsed, [False, True])


class FollowUpTests(unittest.TestCase):
    task = {"follow_up": "Yes, go ahead with what you recommend.", "checks": []}

    def decide(self, checks, completed=True, timed_out=False, task=None):
        with mock.patch.object(ev, "run_checks", return_value=checks) as run_checks:
            answer = ev.needs_follow_up(task or self.task, {"completed": completed, "answer": "Shall I?"}, {"timed_out": timed_out, "started_at": "t"},
                                        "eval-a", Path("."), None, {"apps": ["eval-a"]})
        return answer, run_checks

    def test_follows_up_while_checks_fail_even_if_an_app_exists(self):
        answer, run_checks = self.decide([{"ok": True}, {"ok": False}])
        self.assertTrue(answer)
        self.assertEqual(run_checks.call_args.kwargs["http_deadline_s"], ev.FOLLOW_UP_HTTP_DEADLINE_S)

    def test_no_follow_up_when_done_stopped_or_unconfigured(self):
        self.assertFalse(self.decide([{"ok": True}])[0])
        self.assertFalse(self.decide([{"ok": False}], completed=False)[0])
        self.assertFalse(self.decide([{"ok": False}], timed_out=True)[0])
        self.assertFalse(self.decide([{"ok": False}], task={"checks": []})[0])


class FollowUpLoopTests(unittest.TestCase):
    """run_one answers a stopped agent up to MAX_FOLLOW_UPS times: Codex asked
    "make it public?" after the first "go ahead" (2026-11-19 run)."""

    def run_with(self, agent, decisions, session_ids=("thread-1",)):
        client = ev.Gregale("/bin/echo", "https://api.example.test", "fixture-token")
        client.inventory = mock.Mock(return_value={"apps": set(), "projects": set()})
        client.touched_since = mock.Mock(return_value={})
        client.cli = mock.Mock(return_value=mock.Mock(returncode=0, stdout="", stderr=""))
        options = types.SimpleNamespace(keep_apps=True, keep_dirs=False, mcp_writes=False,
                                        claude_model=None, codex_model=None, gemini_model=None, cursor_model=None)
        task = {"id": "t", "template": "node-hello", "prompt": "Deploy {app}.", "timeout_s": 600, "budget_usd": 1,
                "follow_up": "Yes, go ahead with what you recommend.", "checks": []}
        replies = iter(session_ids)

        def parse(lines):
            return {"completed": True, "answer": "Make it public?", "session_id": next(replies, None), "commands": [], "turns": 1}

        with mock.patch.object(ev, "invoke", return_value=["{}"]) as invoke, \
                mock.patch.dict(ev.PARSERS, {agent: parse}), \
                mock.patch.object(ev, "needs_follow_up", side_effect=list(decisions)) as needs, \
                mock.patch.object(ev, "attribute", return_value=({"apps": [], "projects": []}, [])), \
                mock.patch.object(ev, "run_checks", return_value=[]), \
                mock.patch.object(ev, "home_writes", return_value=[]):
            record = ev.run_one(task, agent, "none", client, options)
        return record, invoke, needs

    def test_answers_twice_then_stops(self):
        record, invoke, needs = self.run_with("codex", [True, True, True])
        self.assertEqual(record["follow_ups"], ev.MAX_FOLLOW_UPS)
        self.assertEqual(invoke.call_count, 1 + ev.MAX_FOLLOW_UPS)
        self.assertEqual(needs.call_count, ev.MAX_FOLLOW_UPS)
        # A resumed Codex run that reports no thread id still resumes the first one.
        self.assertIn("thread-1", invoke.call_args_list[-1].args[0])
        self.assertEqual(record["turns"], 1 + ev.MAX_FOLLOW_UPS)

    def test_stops_once_the_checks_pass(self):
        record, invoke, _ = self.run_with("codex", [True, False])
        self.assertEqual((record["follow_ups"], invoke.call_count), (1, 2))
        record, invoke, _ = self.run_with("codex", [False])
        self.assertNotIn("follow_ups", record)
        self.assertEqual(invoke.call_count, 1)

    def test_an_agent_without_resume_is_unsupported(self):
        record, invoke, _ = self.run_with("gemini", [True])
        self.assertEqual((record["follow_ups"], invoke.call_count), ("unsupported", 1))

    def test_merge_keeps_the_first_answer_and_session_over_two_follow_ups(self):
        first = {"answer": "Shall I deploy?", "session_id": "s1", "turns": 1}
        merged = ev.merge_metrics(ev.merge_metrics(first, {"answer": "Make it public?", "turns": 1}), {"answer": "Live.", "turns": 1})
        self.assertEqual((merged["first_answer"], merged["answer"], merged["session_id"], merged["turns"]), ("Shall I deploy?", "Live.", "s1", 3))


class OutsideWriteTests(unittest.TestCase):
    def test_claude_file_writes_are_recorded(self):
        lines = [json.dumps({"type": "assistant", "message": {"content": [
            {"type": "tool_use", "name": "Write", "input": {"file_path": "/home/u/.npmrc", "content": "x"}},
            {"type": "tool_use", "name": "Edit", "input": {"file_path": "src/app.js"}},
            {"type": "tool_use", "name": "Read", "input": {"file_path": "/etc/hosts"}}]}})]
        metrics = ev.parse_claude(lines)
        self.assertEqual(metrics["written_paths"], ["/home/u/.npmrc", "src/app.js"])
        merged = ev.merge_metrics(metrics, ev.parse_claude(lines))
        self.assertEqual(len(merged["written_paths"]), 4)

    def test_writes_outside_the_run_directory(self):
        with tempfile.TemporaryDirectory() as d:
            run_dir = Path(d) / "run"
            project = run_dir / "project"
            project.mkdir(parents=True)
            outside = ev.writes_outside(["src/app.js", str(run_dir / "notes.md"), str(Path(d) / "elsewhere.txt"), "../../escape.txt"],
                                        run_dir, project)
            self.assertEqual(outside, sorted([str((Path(d) / "elsewhere.txt").resolve()), str((Path(d) / "escape.txt").resolve())]))
            # The agent's own memory (Claude's ~/.claude/projects/*/memory) is not the task's.
            memory = Path(d) / ".claude" / "projects" / "x" / "memory" / "note.md"
            self.assertEqual(ev.writes_outside([str(memory)], run_dir, project, (Path(d) / ".claude",)), [])

    def test_home_writes_skip_old_files_ignored_paths_and_the_run(self):
        with tempfile.TemporaryDirectory() as d:
            home = Path(d)
            for rel in (".npmrc", ".config/tool/settings.json", ".claude/settings.json", "Library/x", ".config/gregale/token",
                        "runs/run/project/app.js", "deep/a/b/c.txt", "old.txt"):
                (home / rel).parent.mkdir(parents=True, exist_ok=True)
                (home / rel).write_text("x")
            os.utime(home / "old.txt", (1, 1))
            since = (home / "old.txt").stat().st_mtime + 1
            found = ev.home_writes(home, since, home / "runs" / "run", max_depth=3)
        self.assertEqual(found, [".config/tool/settings.json", ".npmrc"])


class GradeTests(unittest.TestCase):
    def test_features(self):
        task = {"features": [{"id": "project", "kind": "state", "state": "project_created"},
                             {"id": "preflight", "kind": "command", "pattern": r"gregale\b[^\n]*--dry-run"},
                             {"id": "health-config", "kind": "file", "glob": "gregale.yaml", "pattern": r"health:"},
                             {"id": "logs", "kind": "command", "pattern": r"gregale logs"}]}
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "gregale.yaml").write_text("hosting:\n  health: /actuator/health\n")
            graded = ev.grade_features(task, ["ls", "gregale deploy --dry-run"], Path(d), {"apps": ["ui"], "projects": ["shop"]})
        self.assertEqual(graded, {"features_used": ["project", "preflight", "health-config"], "features_missed": ["logs"], "feature_score": 0.75})
        self.assertIsNone(ev.grade_features({}, [], Path("."), {})["feature_score"])

    def test_feature_unless_first_deploy_live(self):
        # G3 (2026-11-23): Gregale found the uvicorn command itself, so pinning
        # it was a fix the agent did not need.
        task = {"features": [{"id": "start-command-pinned", "kind": "file", "glob": "*", "pattern": "uvicorn", "unless": "first_deploy_live"}]}
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(ev.grade_features(task, [], Path(d), {}, first_live=True)["features_used"], ["start-command-pinned"])
            self.assertEqual(ev.grade_features(task, [], Path(d), {}, first_live=False)["features_missed"], ["start-command-pinned"])

    def test_first_deploy_live(self):
        class G:
            def __init__(self, rows):
                self.rows = rows

            def get(self, path):
                return {"items": self.rows}
        since = "2026-11-23T10:00:00Z"
        first_failed = [{"created_at": "2026-11-23T10:05:00Z", "status": "failed"}, {"created_at": "2026-11-23T10:09:00Z", "status": "live"}]
        first_live = [{"created_at": "2026-11-23T10:05:00Z", "status": "superseded"}, {"created_at": "2026-11-23T10:09:00Z", "status": "live"},
                      {"created_at": "2026-11-22T10:00:00Z", "status": "failed"}]
        self.assertFalse(ev.first_deploy_live(G(first_failed), ["web"], since))
        self.assertTrue(ev.first_deploy_live(G(first_live), ["web"], since))
        self.assertFalse(ev.first_deploy_live(G(first_live), [], since))

    def test_no_traps_grades_the_end_state(self):
        # G4 (voting-app 2026-11-23): a HEALTHCHECK experiment the agent
        # replaced with a working release is not a trap it was left in.
        class G:
            def __init__(self, app, rows):
                self.app, self.rows = app, rows

            def get(self, path):
                return {"items": self.rows} if path.endswith("deployments?limit=20") else self.app
        since = "2026-11-23T10:00:00Z"
        experiment = {"created_at": "2026-11-23T10:05:00Z", "status": "failed", "error_code": "app_startup_timeout"}
        fixed = {"created_at": "2026-11-23T10:09:00Z", "status": "live"}
        task = {"checks": [{"kind": "no_traps"}]}
        recovered = ev.run_checks(task, "s", Path("."), "", G({"status": "active"}, [experiment, fixed]), created={"apps": ["worker"]}, since=since)
        self.assertTrue(recovered[0]["ok"], recovered)
        stuck = ev.run_checks(task, "s", Path("."), "", G({"status": "active"}, [fixed, dict(experiment, created_at="2026-11-23T10:20:00Z")]),
                              created={"apps": ["worker"]}, since=since)
        self.assertEqual(stuck[0]["detail"], "worker:app_startup_timeout")
        parked = {"status": "evicted_cold", "parked_deployment": {"parked_at": "2026-11-23T10:30:00Z", "parked_reason": "liveness_exhausted"}}
        self.assertFalse(ev.run_checks(task, "s", Path("."), "", G(parked, [fixed]), created={"apps": ["worker"]}, since=since)[0]["ok"])
        woken = dict(parked, status="active")
        self.assertTrue(ev.run_checks(task, "s", Path("."), "", G(woken, [fixed]), created={"apps": ["worker"]}, since=since)[0]["ok"])

    def test_prefixed_compose_apps_are_the_runs(self):
        # Codex 2026-11-23: `--prefix retail-sample-` named four apps the
        # agent never typed whole.
        g = ev.CliGregale("/bin/gregale")
        evidence = "gregale project deploy --prefix retail-sample- --yes\ngregale deploy --prefix a"
        touched = {"apps": ["retail-sample-cart", "retail-sample-orders", "other-cart", "alpha"], "projects": []}
        mine, others = ev.attribute(g, touched, evidence)
        self.assertEqual(mine["apps"], ["retail-sample-cart", "retail-sample-orders"])
        self.assertEqual(others["apps"], ["other-cart", "alpha"])

    def test_anti_patterns(self):
        commands = ["curl -sS -X POST https://api.gregale.dev/v1/apps/ui/deployments -F source=@ui.tgz", "tar -czf ui.tgz -C src/ui .",
                    "gregale --non-interactive app delete --yes ui", "gregale key create --name eval", "gregale status", "gregale status",
                    "gregale status", "gregale  status"]
        self.assertEqual(ev.grade_anti_patterns(commands), ["raw_api_call", "manual_tarball", "app_delete", "api_key_minted", "repeated_command"])
        self.assertEqual(ev.grade_anti_patterns(["gregale deploy --project shop", "curl -s https://ui.gregale.dev/", "tar -xzf x.tgz"]), [])
        # Reading the help is not a deletion (2026-11-15 run); deleting after it is.
        self.assertEqual(ev.grade_anti_patterns(["gregale app list --deleted; gregale app delete --help 2>&1 | grep -i grace",
                                                 "gregale project rm -h"]), [])
        self.assertEqual(ev.grade_anti_patterns(["gregale app delete --help; gregale app delete --yes vote"]), ["app_delete"])

    def test_summary_shows_grades(self):
        records = [{"task": "t", "agent": "gemini", "toolset": "none", "success": False, "feature_score": 0.25, "anti_patterns": ["raw_api_call"], "traps": ["ui:liveness_exhausted"]},
                   {"task": "t", "agent": "gemini", "toolset": "none", "success": True, "feature_score": 0.75, "anti_patterns": ["raw_api_call", "manual_tarball"], "traps": []}]
        self.assertIn("| 0.5 | manual_tarball×1, raw_api_call×2 | 1/2 |", ev.summarize(records))


class OwnerResponseTests(unittest.TestCase):
    def test_existing_slug_is_refused_before_setup_or_agent_execution(self):
        client = ev.Gregale("/bin/echo", "https://api.example.test", "fixture-token")
        client.inventory = mock.Mock(return_value={"apps": {"existing"}, "projects": set()})
        client.cli = mock.Mock()
        options = mock.Mock(keep_apps=False, keep_dirs=False)
        task = next(task for task in ev.load_tasks() if task["id"] == "first-hour-private")
        with mock.patch.object(ev, "app_slug", return_value="existing"), mock.patch.object(ev, "invoke") as invoke:
            record = ev.run_one(task, "codex", "none", client, options)
        self.assertFalse(record["success"])
        self.assertIn("already exists", record["error"])
        client.cli.assert_not_called()
        invoke.assert_not_called()

    def test_private_response_requires_exact_identity_status_and_source(self):
        class PrivateGregale:
            def get(self, path):
                if path.endswith("/health"):
                    return {"latest_deployment_id": "d2", "serving_deployment_ids": ["d2"]}
                if "/deployments?" in path:
                    return {"items": [{"id": "d2", "app_id": "a1", "status": "live"}, {"id": "d1", "app_id": "a1", "status": "live"}]}
                return {"id": "a1", "require_authn": True}

            def cli(self, *args, **kwargs):
                self.args = args
                return mock.Mock(returncode=0, stdout=json.dumps(self.evidence))

        original = {"app_id": "a1", "deployment_id": "d2", "scope": "default", "instance_id": "1" * 32,
                    "verified": True, "public_access_verified": False, "http_status": 200, "body": "first-hour-v2"}
        for mutation in ({}, {"deployment_id": "d1"}, {"app_id": "other"}, {"http_status": 401}, {"http_status": 200.5},
                         {"public_access_verified": True}, {"verified": False}, {"instance_id": "0" * 32},
                         {"body": "old source"}, {"body": "x" * 4097}, {"scope": "preview"}):
            with self.subTest(mutation=mutation):
                client = PrivateGregale()
                client.evidence = {**original, **mutation}
                ok, detail = ev.owner_response_check(client, "test-app", {"contains": "first-hour-v2", "min_deployments": 2})
                self.assertEqual(ok, not mutation)
                self.assertEqual(client.args, ("release", "verify", "d2", "--app", "test-app", "--path", "/"))
                self.assertNotIn(client.evidence["body"], detail)

    def test_public_app_is_not_graded_as_private(self):
        client = mock.Mock()
        client.get.return_value = {"id": "a1", "require_authn": False}
        self.assertFalse(ev.owner_response_check(client, "test-app", {})[0])
        client.cli.assert_not_called()

    def test_edit_cannot_replace_the_original_app(self):
        client = mock.Mock()
        client.get.return_value = {"id": "replacement", "require_authn": True}
        self.assertFalse(ev.owner_response_check(client, "test-app", {}, expected_app_id="original")[0])
        client.cli.assert_not_called()

    def test_staged_release_is_not_a_serving_update(self):
        client = mock.Mock()
        client.get.side_effect = [
            {"id": "a1", "require_authn": True},
            {"items": [{"id": "d2", "app_id": "a1", "status": "live"}]},
            {"latest_deployment_id": "d2", "serving_deployment_ids": ["d1"]},
        ]
        self.assertFalse(ev.owner_response_check(client, "test-app", {})[0])
        client.cli.assert_not_called()


if __name__ == "__main__":
    unittest.main()
