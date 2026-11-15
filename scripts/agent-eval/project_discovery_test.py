import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from project_discovery import discovery_checks, fingerprints


class DiscoveryTests(unittest.TestCase):
    def test_success_requires_returned_mcp_evidence_and_unchanged_files(self):
        assessment = {"schema_version": 1, "workloads": [{"name": "web"}, {"name": "api"}], "dependencies": [{"kind": "postgres"}], "features": [{"id": x} for x in ("managed-postgres", "private-services", "project-releases")]}
        workflow = {"schema_version": 1, "task": "deploy_project", "steps": [{"reason": "project_deploy_requires_cli", "status": "unsupported"}]}
        for agent in ("claude", "codex"):
            with self.subTest(agent=agent), tempfile.TemporaryDirectory() as directory:
                project = Path(directory)
                (project / "compose.yaml").write_text("fixture")
                before = fingerprints(project)
                lines = []
                tools = {}
                for i, (name, value) in enumerate((("gregale_assess_project", assessment), ("gregale_get_workflow", workflow))):
                    tools["mcp__gregale__" + name] = 1
                    result = {"content": [{"type": "text", "text": json.dumps(value)}]}
                    if agent == "codex":
                        lines.append(json.dumps({"type": "item.completed", "item": {"type": "mcp_tool_call", "server": "gregale", "tool": name, "status": "completed", "result": result}}))
                    else:
                        lines.append(json.dumps({"type": "assistant", "message": {"content": [{"type": "tool_use", "id": str(i), "name": "mcp__gregale__" + name}]}}))
                        lines.append(json.dumps({"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": str(i), "content": result["content"]}]}}))
                metrics = {"tool_calls": tools, "commands": []}
                created = {"apps": [], "projects": []}
                check = lambda source: discovery_checks(source, metrics, project, before, created)
                self.assertTrue(all(c["ok"] for c in check(lines)))
                self.assertFalse(check([])[0]["ok"], "tool names and a confident answer are insufficient")
                (project / "new-file").write_text("changed")
                self.assertFalse(check(lines)[1]["ok"])
                (project / "new-file").unlink()
                metrics["tool_calls"]["mcp__gregale__gregale_apply_provision"] = 1
                self.assertFalse(check(lines)[1]["ok"])

    def test_failed_tool_results_do_not_establish_success(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            lines = [json.dumps({"type": "item.completed", "item": {"type": "mcp_tool_call", "server": "gregale", "tool": "gregale_assess_project", "status": "completed", "result": {"isError": True, "structuredContent": {"schema_version": 1}}}})]
            self.assertFalse(discovery_checks(lines, {}, project, {}, {})[0]["ok"])

    def test_agent_errors_and_non_object_events_fail_cleanly(self):
        with tempfile.TemporaryDirectory() as directory:
            lines = ['{"type":"error","message":"model unavailable"}', '[]', 'null', 'not-json']
            self.assertFalse(discovery_checks(lines, {}, Path(directory), {}, {})[0]["ok"])


if __name__ == "__main__":
    unittest.main()
