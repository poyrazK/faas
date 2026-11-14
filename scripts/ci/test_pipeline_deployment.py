#!/usr/bin/env python3
"""Pipeline workers converge after unit and database role changes."""
import pathlib
import unittest

import yaml

ROLE = pathlib.Path(__file__).resolve().parents[2] / 'deploy/ansible/roles/control_plane_service'
WORKERS = ('faas-apid-data-pipeline-worker', 'faas-apid-data-pipeline-alert-worker')


class PipelineDeploymentTest(unittest.TestCase):
    def test_unit_and_role_changes_reload_and_restart_only_active_workers(self):
        defaults = yaml.safe_load((ROLE / 'defaults/main.yml').read_text())
        tasks = yaml.safe_load((ROLE / 'tasks/main.yml').read_text())
        handlers = yaml.safe_load((ROLE / 'handlers/main.yml').read_text())
        for worker in WORKERS:
            with self.subTest(worker=worker):
                topic = 'restart ' + worker
                role = next(row for row in defaults['faas_postgres_runtime_role_units'] if row['unit'] == worker)
                self.assertEqual(role['restart'], topic)
                unit = next(task for task in tasks if task.get('ansible.builtin.copy', {}).get('src') == worker + '.service')
                self.assertEqual(unit['notify'], topic)
                listening = [handler for handler in handlers if topic in ([handler.get('listen')] if isinstance(handler.get('listen'), str) else handler.get('listen', []))]
                reload_index = next(i for i, h in enumerate(listening) if h.get('ansible.builtin.systemd_service', {}).get('daemon_reload'))
                restart_index = next(i for i, h in enumerate(listening) if h.get('ansible.builtin.command') == 'systemctl try-restart ' + worker + '.service')
                self.assertLess(reload_index, restart_index)
                self.assertIn('faas_join_defer_service_handlers', listening[restart_index]['when'])


if __name__ == '__main__':
    unittest.main()
