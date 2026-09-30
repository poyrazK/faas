#!/usr/bin/env python3
"""Render UDP deployment contracts; never apply a firewall or start a service."""
import pathlib
import unittest

import jinja2
import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2]
ROLE = ROOT / 'deploy/ansible/roles/gatewayd_public_service'


class UDPDeploymentTest(unittest.TestCase):
    def render_env(self, **changes):
        values = yaml.safe_load((ROLE / 'defaults/main.yml').read_text())
        values.update(changes)
        env = jinja2.Environment()
        env.filters['bool'] = bool
        env.filters['ternary'] = lambda value, yes, no: yes if value else no
        text = env.from_string((ROLE / 'templates/udpd.env.j2').read_text()).render(**values)
        return dict(line.split('=', 1) for line in text.splitlines() if line and not line.startswith('#'))

    def firewall(self, enabled, sources):
        text = (ROOT / 'deploy/ansible/roles/nftables/templates/policy_nftables.conf.j2').read_text()
        return jinja2.Template(text).render(public_iface='eth0', masquerade_cidr='10.100.0.0/16',
            faas_udpd_enabled=enabled, faas_udpd_allowed_cidrs=sources)

    def test_default_exposes_no_udp(self):
        self.assertEqual(self.render_env()['FAAS_UDPD_ENABLED'], '0')
        self.assertEqual(self.render_env()['FAAS_UDPD_ALLOWED_SOURCE_CIDRS'], '')
        for enabled, sources in [(False, []), (False, ['192.0.2.0/24']), (True, [])]:
            self.assertNotIn('UDP app listeners', self.firewall(enabled, sources))

    def test_runtime_and_firewall_use_same_sources(self):
        sources = ['192.0.2.0/24', '198.51.100.1/32']
        values = self.render_env(faas_udpd_enabled=True, faas_udpd_allowed_cidrs=sources,
            faas_udpd_vmmd_tls_ca_path='/etc/faas/pki/ca.pem')
        self.assertEqual(values['FAAS_UDPD_ENABLED'], '1')
        self.assertEqual(values['FAAS_UDPD_ALLOWED_SOURCE_CIDRS'], ','.join(sources))
        self.assertEqual(values['FAAS_UDPD_VMMD_TLS_CA_PATH'], '/etc/faas/pki/ca.pem')
        rules = [line.strip() for line in self.firewall(True, sources).splitlines() if 'UDP app listeners' in line]
        self.assertEqual(rules, [f'ip saddr {cidr} udp dport 40000-49999 accept comment "UDP app listeners"' for cidr in sources])

    def test_systemd_units_load_same_environment(self):
        installed = (ROLE / 'files/faas-gatewayd-public.service').read_text()
        canonical = (ROOT / 'deploy/systemd/faas-gatewayd-public.service').read_text()
        self.assertEqual(installed, canonical)
        self.assertIn('EnvironmentFile=-/etc/faas/udpd.env', canonical)


if __name__ == '__main__':
    unittest.main()
