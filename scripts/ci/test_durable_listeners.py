#!/usr/bin/env python3
"""Render the durable-listener drop-ins (ADR-2026101801); never touch a host.

A socket unit must carry exactly the address its daemon serves: the daemon
refuses an inherited socket it does not serve. These tests render the role
templates and check that one variable drives both sides.
"""
import pathlib
import re
import unittest

import jinja2
import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2]
ROLES = ROOT / 'deploy/ansible/roles'
CONTROL = ROLES / 'control_plane_service'
COMPUTE = ROLES / 'gatewayd_internal_service'


def ansible_bool(value):
    if isinstance(value, str):
        return value.lower() in ('yes', 'on', '1', 'true')
    return value is True or value == 1


def render(path, **values):
    env = jinja2.Environment()
    env.filters['regex_replace'] = lambda value, pattern, repl: re.sub(pattern, repl, value)
    env.filters['bool'] = ansible_bool
    env.filters['default'] = lambda value, default='': default if value is None or isinstance(value, jinja2.Undefined) else value
    return env.from_string(path.read_text()).render(**values)


def directives(text, key):
    return [line.split('=', 1)[1] for line in text.splitlines() if line.startswith(key + '=')]


def task(tasks, name):
    for entry in tasks:
        if entry.get('name', '').endswith(name):
            return entry
    raise AssertionError('missing task: ' + name)


class ScheddListenerTest(unittest.TestCase):
    def rendered(self, address):
        service = render(CONTROL / 'templates/50-faas-schedd-durable-listener.conf.j2', faas_schedd_listen_addr=address)
        socket = render(CONTROL / 'templates/50-faas-schedd-listen.conf.j2', faas_schedd_listen_addr=address)
        return service, socket

    def test_tcp_listen_addr_produces_a_matching_socket(self):
        service, socket = self.rendered('tcp://0.0.0.0:9091')
        self.assertEqual(directives(service, 'Environment'), ['FAAS_SCHEDD_LISTEN_ADDR=tcp://0.0.0.0:9091'])
        # The empty entry resets the unit's default unix listener.
        self.assertEqual(directives(socket, 'ListenStream'), ['', '0.0.0.0:9091'])
        self.assertEqual(directives(service, 'Wants'), ['faas-schedd.socket'])
        self.assertEqual(directives(service, 'After'), ['faas-schedd.socket'])

    def test_unix_listen_addr_produces_a_matching_socket(self):
        service, socket = self.rendered('unix:///run/faas/schedd.sock')
        self.assertEqual(directives(service, 'Environment'), ['FAAS_SCHEDD_LISTEN_ADDR=unix:///run/faas/schedd.sock'])
        self.assertEqual(directives(socket, 'ListenStream'), ['', '/run/faas/schedd.sock'])

    def test_socket_is_off_unless_the_address_is_set(self):
        self.assertEqual(yaml.safe_load((CONTROL / 'defaults/main.yml').read_text())['faas_schedd_listen_addr'], '')
        tasks = yaml.safe_load((CONTROL / 'tasks/main.yml').read_text())
        self.assertEqual(task(tasks, 'render the durable schedd listener')['when'], 'faas_schedd_listen_addr | length > 0')
        self.assertEqual(task(tasks, 'remove the durable schedd listener when unset')['when'], 'faas_schedd_listen_addr | length == 0')
        enable = task(tasks, 'enable the durable schedd socket with its listener')
        self.assertEqual(enable['ansible.builtin.systemd_service']['enabled'], '{{ faas_schedd_listen_addr | length > 0 }}')
        self.assertNotIn('state', enable['ansible.builtin.systemd_service'])

    def test_static_units_do_not_require_the_socket(self):
        for unit in ('control_plane_service/files/faas-schedd.service', 'gatewayd_internal_service/files/faas-gatewayd-internal.service',
                     'gatewayd_internal_service/files/faas-realtimed.service'):
            self.assertNotIn('.socket', (ROLES / unit).read_text(), unit)


class ComputeListenersTest(unittest.TestCase):
    def test_off_by_default(self):
        self.assertIs(yaml.safe_load((COMPUTE / 'defaults/main.yml').read_text())['faas_compute_durable_listeners'], False)
        tasks = yaml.safe_load((COMPUTE / 'tasks/main.yml').read_text())
        self.assertEqual(task(tasks, 'render the durable listener drop-ins')['when'], 'faas_compute_durable_listeners | bool')
        self.assertEqual(task(tasks, 'remove the durable listener drop-ins when off')['when'], 'not (faas_compute_durable_listeners | bool)')

    def test_service_drop_ins_want_their_own_socket(self):
        tasks = yaml.safe_load((COMPUTE / 'tasks/main.yml').read_text())
        for item in task(tasks, 'render the durable listener drop-ins')['loop']:
            if item['src'] != '50-faas-durable-listener.conf.j2':
                continue
            text = render(COMPUTE / 'templates' / item['src'], item=item)
            service = pathlib.Path(item['dest']).parent.name.removesuffix('.service.d')
            self.assertEqual(item['socket'], service + '.socket')
            self.assertEqual(directives(text, 'Wants'), [item['socket']])
            self.assertEqual(directives(text, 'After'), [item['socket']])

    def test_data_listener_matches_the_service_listen_address(self):
        for listen, want in (('0.0.0.0:8080', ['0.0.0.0:8080']), ('off', []), (None, [])):
            values = {} if listen is None else {'faas_gateway_listen': listen}
            socket = render(COMPUTE / 'templates/99-faas-data-listen.conf.j2', **values)
            service = render(COMPUTE / 'templates/99-faas-public-listen.conf.j2', **values)
            self.assertEqual(directives(socket, 'ListenStream'), want, listen)
            served = directives(service, 'Environment')[0].removeprefix('FAAS_GATEWAY_LISTEN=')
            if want:
                self.assertEqual(served, want[0])


if __name__ == '__main__':
    unittest.main()
