#!/usr/bin/env python3
"""Render UDP deployment contracts; never apply a firewall or start a service."""
import json
import re
import pathlib
import unittest

import jinja2
import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2]
ROLE = ROOT / 'deploy/ansible/roles/gatewayd_public_service'


def ansible_bool(value):
    # Model the Ansible bool filter for supported inventory values.
    if isinstance(value, str):
        return value.lower() in ('yes', 'on', '1', 'true')
    return value is True or value == 1


class UDPDeploymentTest(unittest.TestCase):
    def render_env(self, **changes):
        values = yaml.safe_load((ROLE / 'defaults/main.yml').read_text())
        values.update(changes)
        env = jinja2.Environment()
        env.filters['to_json'] = json.dumps
        env.filters['bool'] = ansible_bool
        env.filters['ternary'] = lambda value, yes, no: yes if value else no
        text = env.from_string((ROLE / 'templates/udpd.env.j2').read_text()).render(**values)
        values = dict(line.split('=', 1) for line in text.splitlines() if line and not line.startswith('#'))
        return {key: json.loads(value) if value.startswith(chr(34)) else value for key, value in values.items()}

    def firewall(self, enabled, sources):
        text = (ROOT / 'deploy/ansible/roles/nftables/templates/policy_nftables.conf.j2').read_text()
        env = jinja2.Environment()
        env.filters['bool'] = ansible_bool
        return env.from_string(text).render(public_iface='eth0', masquerade_cidr='10.100.0.0/16',
            faas_udpd_enabled=enabled, faas_udpd_allowed_cidrs=sources)

    def test_default_exposes_no_udp(self):
        self.assertEqual(self.render_env()['FAAS_UDPD_ENABLED'], '0')
        self.assertEqual(self.render_env()['FAAS_UDPD_ALLOWED_SOURCE_CIDRS'], '')
        for enabled, sources in [(False, []), (False, ['192.0.2.0/24']), (True, [])]:
            self.assertNotIn('UDP app listeners', self.firewall(enabled, sources))

    def test_string_opt_in_matches_runtime_and_firewall(self):
        sources = ['192.0.2.0/24']
        for enabled in ['false', 'False', '0', 'no', 'off']:
            self.assertEqual(self.render_env(faas_udpd_enabled=enabled, faas_udpd_allowed_cidrs=sources)['FAAS_UDPD_ENABLED'], '0')
            self.assertNotIn('UDP app listeners', self.firewall(enabled, sources))
        for enabled in ['true', 'True', '1', 'yes', 'on']:
            self.assertEqual(self.render_env(faas_udpd_enabled=enabled, faas_udpd_allowed_cidrs=sources)['FAAS_UDPD_ENABLED'], '1')
            self.assertIn('UDP app listeners', self.firewall(enabled, sources))

    def test_runtime_and_firewall_use_same_sources(self):
        sources = ['192.0.2.0/24', '198.51.100.1/32']
        values = self.render_env(faas_udpd_enabled=True, faas_udpd_allowed_cidrs=sources,
            faas_udpd_vmmd_tls_ca_path='/etc/faas/pki/ca.pem')
        self.assertEqual(values['FAAS_UDPD_ENABLED'], '1')
        self.assertEqual(values['FAAS_UDPD_ALLOWED_SOURCE_CIDRS'], ','.join(sources))
        self.assertEqual(values['FAAS_UDPD_VMMD_TLS_CA_PATH'], '/etc/faas/pki/ca.pem')
        rules = [line.strip() for line in self.firewall(True, sources).splitlines() if 'UDP app listeners' in line]
        self.assertEqual(rules, [f'ip saddr {cidr} udp dport 40000-49999 accept comment "UDP app listeners"' for cidr in sources])

    def test_environment_values_cannot_inject_new_assignments(self):
        path = '/etc/faas/pki/key with spaces"\nFAAS_UDPD_ENABLED=1'
        values = self.render_env(faas_udpd_vmmd_tls_key_path=path)
        self.assertEqual(values['FAAS_UDPD_ENABLED'], '0')
        self.assertEqual(values['FAAS_UDPD_VMMD_TLS_KEY_PATH'], path)

    def test_source_validation_rejects_firewall_injection(self):
        tasks = yaml.safe_load((ROOT / 'deploy/ansible/tasks/validate_udp_policy.yml').read_text())
        task = next(t for t in tasks if 'validate each UDP source CIDR' in t.get('name', ''))
        pattern = task['vars']['udp_cidr_pattern']
        for value in ['192.0.2.1/32', '0.0.0.0/0', '255.255.255.255/32']:
            self.assertIsNotNone(re.fullmatch(pattern, value))
        for value in ['999.0.0.1/8', '192.0.2.0/33', '::/0', '192.0.2.0/24\naccept', '192.0.2.0/24; accept', '01.2.3.4/8']:
            self.assertIsNone(re.fullmatch(pattern, value))

    def test_both_roles_validate_policy_before_rendering(self):
        for role in ['gatewayd_public_service', 'nftables']:
            tasks = yaml.safe_load((ROOT / 'deploy/ansible/roles' / role / 'tasks/main.yml').read_text())
            index = next(i for i, task in enumerate(tasks) if task.get('ansible.builtin.include_tasks', '').endswith('/validate_udp_policy.yml'))
            writes = [i for i, task in enumerate(tasks) if 'ansible.builtin.template' in task]
            self.assertTrue(writes)
            # The gateway may render unrelated TCP configuration first.
            if role == 'gatewayd_public_service':
                writes = [i for i in writes if tasks[i]['ansible.builtin.template']['src'] == 'udpd.env.j2']
            self.assertTrue(all(index < i for i in writes))

    def test_systemd_units_load_same_environment(self):
        installed = (ROLE / 'files/faas-gatewayd-public.service').read_text()
        canonical = (ROOT / 'deploy/systemd/faas-gatewayd-public.service').read_text()
        self.assertEqual(installed, canonical)
        self.assertIn('EnvironmentFile=-/etc/faas/udpd.env', canonical)


if __name__ == '__main__':
    unittest.main()
