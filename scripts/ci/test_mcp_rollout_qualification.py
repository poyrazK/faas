"""Local contract checks; these deliberately do not qualify native hosting."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('rollout', ROOT / 'scripts/ops/mcp-hosting-rollout-qualification.py')
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class QualificationTests(unittest.TestCase):
    def test_prepare_isolated_failure_and_handler_fixtures(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'fixtures'
            MODULE.prepare(ROOT / 'cmd/gregale/templates/mcp-node', target)
            for name in ('web', 'web-bad', 'worker-old', 'worker-candidate', 'observer'):
                source = target / name
                config = json.loads((source / 'gregale-mcp.json').read_text())
                self.assertTrue(config['tasks']['enabled'])
                self.assertEqual(config['tasks']['namespace_env'], 'MCP_TASK_NAMESPACE')
                self.assertEqual(config['tasks']['database_url_env'], 'DATABASE_URL')
                self.assertFalse((source / 'node_modules').exists())
                generation = 'old' if name == 'worker-old' else 'candidate'
                self.assertIn("const generation = '" + generation + "'", (source / 'tasks.js').read_text())
            self.assertIn('res.status(503)', (target / 'web-bad/app.js').read_text())
            self.assertNotIn('res.status(503)', (target / 'web/app.js').read_text())
            self.assertIn('mcp_qualification_observer_role', (target / 'observer/tasks-observer.js').read_text())
            with self.assertRaises(ValueError):
                MODULE.prepare(ROOT / 'cmd/gregale/templates/mcp-node', target)

    def test_missing_credentials_are_blocked_and_redacted(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory) / 'blocked.json'
            args = argparse.Namespace(commit='a' * 40, scenario='rollout', evidence=evidence)
            with patch.dict(os.environ, {}, clear=True):
                report = MODULE.qualify(args)
            self.assertEqual(report['status'], 'blocked')
            self.assertFalse(report['native_observations'])
            self.assertEqual(json.loads(evidence.read_text()), report)
            self.assertEqual(evidence.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                MODULE.qualify(args)

    def test_command_errors_do_not_expose_secrets(self):
        with self.assertRaisesRegex(RuntimeError, '^Qualification command failed$'):
            MODULE.execute(['python3', '-c', "import sys; print('postgres://secret'); sys.exit(1)"], dict(os.environ))


if __name__ == '__main__':
    unittest.main()
