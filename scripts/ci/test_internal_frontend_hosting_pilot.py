#!/usr/bin/env python3
"""Bundled pilot adapters: real Unix HTTP, crash journals and isolated provider fixtures."""
from copy import deepcopy
import base64
from contextlib import contextmanager
import http.client
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from urllib.parse import parse_qs, urlsplit

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts/ops'))
from internal_frontend_hosting_pilot import prepare, qualification, recovery_cleanup, runner_args
from internal_frontend_hosting_pilot_common import Journal, PilotError, atomic_json, configuration, private_read
from internal_frontend_hosting_pilot_dns import DNSController
from internal_frontend_hosting_pilot_relay import ProviderController, Relay, expire, local_health, serve

SID = '11111111-1111-4111-8111-111111111111'
DID = '22222222-2222-4222-8222-222222222222'
RUN = '33333333-3333-4333-8333-333333333333'
HOST = '44444444-4444-4444-8444-444444444444'
APP = '55555555-5555-4555-8555-555555555555'


def config(root):
    return {'version': 1, 'api_origin': 'https://api.platform.example', 'account_id': SID, 'origin_id': 'e' * 64,
            'worker_name': 'gregale-internal-pilot', 'hostname': 'pilot.customer.example', 'hosting_domain': 'hosting.example',
            'cf_account_id': 'a' * 32, 'hosting_zone_id': 'b' * 32, 'access_policy_id': DID,
            'dns_account_id': 'c' * 32, 'dns_zone_id': 'd' * 32, 'dns_zone_name': 'customer.example',
            'access_team': 'pilot.cloudflareaccess.com', 'analytics_dataset': 'gregale_internal_pilot_traffic',
            'dns_token_file': str(root / 'dns-token'), 'hosting_read_token_file': str(root / 'read-token'),
            'domain_access_token_file': str(root / 'access-token')}


def setup(root):
    cfg = config(root)
    source = root / 'source.json'
    atomic_json(source, cfg)
    result = prepare(source, root / 'run')
    return Journal(root / 'run'), result


def identity(journal):
    return {k: journal.config[k] for k in ('version', 'api_origin', 'account_id', 'origin_id', 'worker_name', 'hostname')} | {'run_id': RUN}


def sync_request(journal):
    return {**identity(journal), 'site_id': SID, 'domain_id': DID, 'access_aud': 'f' * 64,
            'records': [{'type': 'TXT', 'name': '_gregale-hosting.' + journal.config['hostname'], 'value': 'gregale-hosting=' + 'e' * 64},
                        {'type': 'CNAME', 'name': journal.config['hostname'], 'value': 'customers.hosting.example'}]}


def scope(journal, mode):
    return {k: journal.config[k] for k in ('version', 'api_origin', 'account_id', 'origin_id', 'worker_name', 'hostname')} | {
        'site_id': SID, 'domain_id': DID, 'mode': mode, 'run_id': RUN, 'duration': 480}


class CloudflareFixture:
    def __init__(self, cfg):
        self.cfg = cfg
        self.dns = {}
        self.hosts = {}
        self.apps = {}
        self.calls = []
        self.sequence = 0
        self.lose_create = self.lose_delete = False

    def cloudflare(self, token, method, path, value=None):
        self.calls.append((method, path, deepcopy(value)))
        url = urlsplit(path)
        cfg = self.cfg
        if url.path == '/zones/' + cfg['dns_zone_id']:
            result = {'name': cfg['dns_zone_name'], 'account': {'id': cfg['dns_account_id']}}
        elif url.path == '/zones/' + cfg['hosting_zone_id']:
            result = {'name': cfg['hosting_domain'], 'account': {'id': cfg['cf_account_id']}}
        elif '/dns_records' in url.path:
            if method == 'GET':
                name = parse_qs(url.query)['name'][0]
                result = [deepcopy(r) for r in self.dns.values() if r['name'] == name]
            elif method == 'POST':
                self.sequence += 1
                record = {**value, 'id': f'{self.sequence:032x}', 'proxied': value.get('proxied', False)}
                self.dns[record['id']] = record
                if self.lose_create:
                    self.lose_create = False
                    raise PilotError('Pilot HTTPS request failed')
                result = deepcopy(record)
            else:
                rid = url.path.rsplit('/', 1)[1]
                self.dns.pop(rid)
                if self.lose_delete:
                    self.lose_delete = False
                    raise PilotError('Pilot HTTPS request failed')
                result = {'id': rid}
        elif url.path.endswith('/custom_hostnames'):
            if method == 'GET':
                result = list(self.hosts.values())
            else:
                self.hosts[HOST] = {'id': HOST, 'hostname': cfg['hostname'], 'ssl': {'status': 'active'}}
                result = deepcopy(self.hosts[HOST])
        elif '/custom_hostnames/' in url.path:
            result = deepcopy(self.hosts[url.path.rsplit('/', 1)[1]])
        elif url.path.endswith('/access/apps'):
            result = [] if method == 'GET' else {'id': APP, 'domain': cfg['hostname'], 'type': 'self_hosted'}
        else:
            raise AssertionError('Unexpected fixture API path')
        return {'success': True, 'result': result, 'result_info': {'total_pages': 1}}

    def request(self, method, url, headers=None, body=None, timeout=20):
        if url.endswith('/healthz'):
            return 200, {}, b'{"ok":true}'
        self.calls.append(('headers', deepcopy(headers)))
        if url.endswith('/analytics_engine/sql'):
            return 200, {}, b'{"data":[]}'
        reply = self.cloudflare('', method, url.split('/client/v4')[1], json.loads(body) if body else None)
        return 200, {}, json.dumps(reply).encode()


@contextmanager
def running(journal, http):
    ready, stopping = threading.Event(), threading.Event()
    thread = threading.Thread(target=serve, args=(journal, http, ready, stopping))
    thread.start()
    if not ready.wait(3):
        stopping.set()
        thread.join(3)
        raise AssertionError('Relay failed to start')
    try:
        yield
    finally:
        stopping.set()
        thread.join(3)
        if thread.is_alive():
            raise AssertionError('Relay thread leaked')


class PilotTests(unittest.TestCase):
    def test_prepare_is_local_private_and_never_reuses_a_directory(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            root = Path(directory)
            journal, reply = setup(root)
            self.assertEqual(reply['apid_environment']['FAAS_FRONTEND_HOSTING_PILOT_ENABLED'], 'true')
            self.assertEqual(journal.root.stat().st_mode & 0o777, 0o700)
            self.assertEqual((journal.root / 'journal.json').stat().st_mode & 0o777, 0o600)
            self.assertEqual((journal.root / 'domain-controller').stat().st_mode & 0o777, 0o700)
            self.assertNotIn('SECRET', json.dumps(reply))
            with self.assertRaises(PilotError):
                prepare(root / 'source.json', journal.root)

    def test_prepared_configuration_is_immutable_and_relay_ownership_exclusive(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            with running(journal, cf):
                with self.assertRaises(PilotError):
                    serve(journal, cf)
                self.assertTrue(local_health(journal.root)['ready'])
            changed = {**journal.config, 'dns_token_file': str(Path(directory) / 'different-token')}
            atomic_json(journal.root / 'config.json', changed)
            with self.assertRaises(PilotError):
                Journal(journal.root)

    def test_configuration_rejects_shared_accounts_foreign_targets_and_unknown_fields(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            cfg = config(Path(directory))
            for change in ({'dns_account_id': cfg['cf_account_id']}, {'hostname': 'customer.example'},
                           {'hostname': 'pilot.hosting.example'}, {'api_origin': 'http://api.platform.example'},
                           {'api_origin': 'https://user:SECRET@api.platform.example'}, {'unexpected': True}):
                with self.subTest(change=change), self.assertRaises(PilotError):
                    configuration({**cfg, **change})

    def test_clear_without_any_durable_dns_intent_is_a_provider_free_noop(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            self.assertTrue(DNSController(journal, cf).run('clear', sync_request(journal))['cleared'])
            self.assertEqual(cf.calls, [])

    def test_dns_writes_are_owned_idempotent_and_clear_only_own_records(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            adapter = DNSController(journal, cf)
            request = sync_request(journal)
            self.assertTrue(adapter.run('describe', request)['disposable_hostname'])
            self.assertTrue(adapter.run('sync', request)['applied'])
            self.assertTrue(adapter.run('sync', request)['applied'])
            self.assertEqual(len(cf.dns), 2)
            cf.dns['0' * 32] = {'id': '0' * 32, 'type': 'TXT', 'name': 'unrelated.customer.example', 'content': 'preserve'}
            self.assertTrue(adapter.run('clear', request)['cleared'])
            self.assertTrue(adapter.run('clear', request)['cleared'])
            self.assertEqual(list(cf.dns), ['0' * 32])
            with self.assertRaises(PilotError):
                adapter.run('sync', request)

    def test_dns_lost_create_and_delete_responses_recover_without_duplicate_writes(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            adapter = DNSController(journal, cf)
            request = sync_request(journal)
            cf.lose_create = True
            with self.assertRaises(PilotError):
                adapter.run('sync', request)
            self.assertTrue(adapter.run('sync', request)['applied'])
            self.assertEqual(sum(c[0] == 'POST' for c in cf.calls), 2)
            cf.lose_delete = True
            with self.assertRaises(PilotError):
                adapter.run('clear', request)
            self.assertTrue(adapter.run('clear', request)['cleared'])
            self.assertEqual(sum(c[0] == 'DELETE' for c in cf.calls), 2)

    def test_dns_foreign_changed_or_missing_uncertain_records_fail_closed(self):
        for mode in ('foreign', 'changed', 'absent_uncertain', 'another_run', 'escaped_name'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory(prefix='hp-') as directory:
                journal, _ = setup(Path(directory))
                cf = CloudflareFixture(journal.config)
                adapter = DNSController(journal, cf)
                request = sync_request(journal)
                if mode == 'foreign':
                    cf.dns['0' * 32] = {'id': '0' * 32, 'type': 'CNAME', 'name': journal.config['hostname'], 'content': 'foreign.example'}
                elif mode == 'escaped_name':
                    request['records'][0]['name'] = 'production.example'
                else:
                    adapter.run('sync', request)
                    if mode == 'changed':
                        next(iter(cf.dns.values()))['comment'] = 'foreign'
                    elif mode == 'absent_uncertain':
                        with journal.locked() as state:
                            entry = next(iter(state['dns']['records'].values()))
                            entry['id'] = None
                            cf.dns.clear()
                    else:
                        request['run_id'] = SID
                with self.assertRaises(PilotError):
                    adapter.run('clear' if mode in ('changed', 'absent_uncertain') else 'sync', request)
                self.assertFalse(any(c[0] == 'DELETE' for c in cf.calls))

    def test_access_reader_checks_audience_issuer_expiry_and_private_file(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            adapter = DNSController(journal, cf)
            request = sync_request(journal)
            adapter.run('sync', request)
            path = Path(journal.config['domain_access_token_file'])
            claims = {'aud': ['f' * 64], 'iss': 'https://' + journal.config['access_team'], 'exp': int(time.time()) + 600}
            def write(value):
                payload = base64.urlsafe_b64encode(json.dumps(value).encode()).decode().rstrip('=')
                token = 'fixture.' + payload + '.fixture-signature'
                path.write_text(token)
                path.chmod(0o600)
                return token
            token = write(claims)
            self.assertEqual(adapter.run('token', request)['access_token'], token)
            for change in ({'aud': ['e' * 64]}, {'iss': 'https://foreign.example'}, {'exp': 1}):
                write({**claims, **change})
                with self.assertRaises(PilotError):
                    adapter.run('token', request)
            write(claims)
            path.chmod(0o644)
            with self.assertRaises(PilotError):
                adapter.run('token', request)
            self.assertNotIn(token, (journal.root / 'journal.json').read_text())

    def test_private_relay_loses_one_real_create_and_counts_provider_receipts(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            with running(journal, cf):
                controller = ProviderController(journal, cf)
                request = scope(journal, 'create_response')
                self.assertTrue(controller.run('enter', request)['fault_active'])
                DNSController(journal, cf).run('sync', sync_request(journal))
                relay = Relay(journal, cf)
                target = '/client/v4/zones/' + journal.config['hosting_zone_id'] + '/custom_hostnames'
                body = json.dumps({'hostname': journal.config['hostname'], 'ssl': {'method': 'txt', 'type': 'dv'}}).encode()
                status, _ = relay.request('POST', target, {'authorization': 'Bearer SECRET-DOMAIN'}, body)
                self.assertEqual(status, 503)
                status = controller.run('status', request)
                self.assertEqual((status['create_requests'], status['provider_objects'], status['dropped_responses']), (1, 1, 1))
                self.assertTrue(status['api_running'])
                self.assertFalse(controller.run('leave', request)['fault_active'])
                self.assertEqual(controller.run('inspect', request)['create_requests'], 1)
                self.assertNotIn('SECRET', (journal.root / 'journal.json').read_text())
                connection = http.client.HTTPConnection('hosting-pilot.local')
                import socket
                sock = socket.socket(socket.AF_UNIX)
                sock.connect(str(journal.root / 'relay.sock'))
                connection.sock = sock
                connection.request('GET', '/client/v4/zones/' + journal.config['hosting_zone_id'], headers={'Authorization': 'Bearer SECRET-DOMAIN'})
                response = connection.getresponse()
                self.assertEqual(response.status, 200)
                response.read()
                connection.close()
                self.assertEqual((journal.root / 'relay.sock').stat().st_mode & 0o777, 0o600)

    def test_provider_faults_reject_overlap_foreign_scope_and_restore_idempotently(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            DNSController(journal, cf).run('sync', sync_request(journal))
            with running(journal, cf):
                controller = ProviderController(journal, cf)
                request = scope(journal, 'domain_api')
                controller.run('enter', request)
                with self.assertRaises(PilotError):
                    controller.run('enter', {**request, 'run_id': SID})
                with self.assertRaises(PilotError):
                    controller.run('leave', {**request, 'site_id': HOST})
                relay = Relay(journal, cf)
                target = '/client/v4/zones/' + journal.config['hosting_zone_id']
                self.assertEqual(relay.request('GET', target, {'authorization': 'Bearer SECRET'}, b'')[0], 503)
                self.assertEqual(controller.run('status', request)['failed_requests'], 1)
                controller.run('leave', request)
                controller.run('leave', request)
                self.assertEqual(relay.request('GET', target, {'authorization': 'Bearer SECRET'}, b'')[0], 200)
                self.assertEqual(controller.run('inspect', request)['successful_requests_after_restore'], 1)

    def test_relay_rejects_unrelated_endpoints_queries_and_mutations(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            relay = Relay(journal, cf)
            for method, path, body in [('GET', 'https://evil.example/x', b''), ('DELETE', '/client/v4/zones/foreign', b''),
                ('POST', '/client/v4/zones/' + journal.config['hosting_zone_id'] + '/custom_hostnames', b'{"hostname":"foreign.example"}'),
                ('POST', '/client/v4/accounts/' + journal.config['cf_account_id'] + '/analytics_engine/sql', b'SELECT * FROM other')]:
                with self.subTest(path=path), self.assertRaises(PilotError):
                    relay.request(method, path, {'authorization': 'Bearer SECRET'}, body)
            self.assertEqual(cf.calls, [])

    def test_analytics_fault_keeps_collection_routes_outside_its_scope(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            cf = CloudflareFixture(journal.config)
            with running(journal, cf):
                controller = ProviderController(journal, cf)
                request = {**scope(journal, 'analytics_query'), 'hostname': '', 'domain_id': ''}
                controller.run('enter', request)
                relay = Relay(journal, cf)
                # Use the production query grammar with fixed, non-secret fixture values.
                source = (ROOT / 'pkg/hosting/analytics.go').read_text()
                template = source.split('const trafficSQL = `', 1)[1].split('`', 1)[0]
                sql = (template % (journal.config['analytics_dataset'], SID, journal.config['origin_id'],
                                   '2026-10-15 12:00:00', '2026-10-16 12:00:00', 2048)).encode()
                path = '/client/v4/accounts/' + journal.config['cf_account_id'] + '/analytics_engine/sql'
                with self.assertRaises(PilotError):
                    relay.request('POST', path, {'authorization': 'Bearer SECRET'}, sql + b' UNION SELECT * FROM foreign')
                self.assertEqual(relay.request('POST', path, {'authorization': 'Bearer SECRET'}, sql)[0], 503)
                self.assertEqual(controller.run('status', request)['failed_requests'], 1)
                controller.run('leave', request)
                self.assertEqual(relay.request('POST', path, {'authorization': 'Bearer SECRET'}, sql)[0], 200)
                self.assertEqual(controller.run('inspect', request)['successful_requests_after_restore'], 1)

    def test_independent_daemon_watchdog_restores_after_controller_exit(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            command = [sys.executable, str(ROOT / 'scripts/ops/internal_frontend_hosting_pilot.py'), 'serve', '--directory', str(journal.root)]
            process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                deadline = time.monotonic() + 5
                while not (journal.root / 'relay.sock').exists():
                    self.assertIsNone(process.poll())
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.02)
                controller = subprocess.run([str(journal.root / 'provider-controller'), 'describe'],
                    input=json.dumps({**scope(journal, ''), 'site_id': '', 'domain_id': '', 'run_id': '', 'duration': 0}).encode(), capture_output=True, timeout=5)
                self.assertEqual(controller.returncode, 0, controller.stderr)
                with journal.locked() as state:
                    state['active'] = RUN
                    state['faults'][RUN] = {'scope': scope(journal, 'create_response'), 'restore_at': time.time() + .2, 'restored_at': None}
                time.sleep(.6)
                self.assertIsNone(local_health(journal.root)['active'])
                with journal.locked() as state:
                    self.assertIsNotNone(state['faults'][RUN]['restored_at'])
            finally:
                process.send_signal(signal.SIGTERM)
                stdout, stderr = process.communicate(timeout=5)
            self.assertEqual(process.returncode, 0, stderr)
            self.assertEqual(stdout, b'')
            self.assertFalse((journal.root / 'relay.sock').exists())

    def test_generated_controllers_and_cli_errors_never_print_input_secrets(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            journal, _ = setup(Path(directory))
            result = subprocess.run([str(journal.root / 'domain-controller'), 'sync'], input=b'{"SECRET":', capture_output=True, timeout=5)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn(b'SECRET', result.stdout + result.stderr)

    def test_qualify_uses_existing_runner_and_never_invents_credentials_or_flags(self):
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            root = Path(directory)
            journal, _ = setup(root)
            args = SimpleNamespace(infrastructure=root, binary='gregale', terraform='terraform', aws='aws', project='test',
                environment='test', evidence=root / 'evidence.json', edge_timeout=900, cleanup_timeout=900,
                check_usage=False, check_traffic=False, provider_failures=False, domain_recovery_review=None)
            with patch.dict(os.environ, {'FAAS_API': journal.config['api_origin']}), \
                 patch('internal_frontend_hosting_pilot.local_health', return_value={'ready': True}), \
                 patch('internal_frontend_hosting_pilot.load_runner', return_value=SimpleNamespace(
                     Infrastructure=lambda *a: SimpleNamespace(output=lambda: {**journal.config, 'account_id': journal.config['cf_account_id']}))), \
                 patch('internal_frontend_hosting_pilot.subprocess.run', return_value=SimpleNamespace(returncode=0)) as run:
                self.assertEqual(qualification(journal, args), 0)
                command = run.call_args.args[0]
                self.assertNotIn('--check-domain-recovery', command)
                self.assertNotIn('--check-worker-switch', command)
                self.assertNotIn('apply', command)
                self.assertEqual(command[2:], runner_args(journal, args))

    def test_recovery_cleanup_restores_journal_then_checks_heads_before_dns_clear(self):
        from test_internal_frontend_hosting_qualification import MODULE
        for corrupt in (False, True):
            with self.subTest(corrupt=corrupt), tempfile.TemporaryDirectory(prefix='hp-') as directory, patch('builtins.print'):
                root = Path(directory)
                journal, _ = setup(root)
                cf = CloudflareFixture(journal.config)
                request = sync_request(journal)
                DNSController(journal, cf).run('sync', request)
                deployment = {'account_id': journal.config['cf_account_id'], 'origin_id': journal.config['origin_id'],
                    'worker_name': journal.config['worker_name'], 'hosting_domain': journal.config['hosting_domain'], 'bucket': 'gregale-internal-pilot'}
                class CleanupQualification(MODULE.Qualification):
                    def __init__(self, args):
                        super().__init__(args, env={'FAAS_TOKEN': 'SECRET-API'})
                        self.infrastructure = SimpleNamespace(output=lambda: deployment)
                    def cli(self, *args, **kwargs):
                        return {'id': journal.config['account_id']}
                    def api_response(self, *args, **kwargs):
                        return 404, {}, b'{"code":"not_found"}'
                    def storage_count(self, prefix):
                        return 0
                    def storage_route_head(self):
                        return {'version': 2, 'deleted': True, 'enabled': False, 'expires_at': 0, 'site_id': SID,
                            'hostname': SID + '.hosting.example', 'origin_id': journal.config['origin_id'],
                            'generation': 3, 'active_release_id': '', 'ready_releases': []}
                    def storage(self, *args):
                        value = {'version': 1, 'deleted': True, 'enabled': False, 'id': DID, 'site_id': SID,
                            'hostname': journal.config['hostname'], 'canonical_hostname': SID + '.hosting.example',
                            'origin_id': journal.config['origin_id'], 'generation': 3}
                        Path(args[-1]).write_text(json.dumps({} if corrupt else value))
                        return {}
                    def domain_control(self, action, records=None):
                        return DNSController(journal, cf).run(action, {**request, 'run_id': self.domain_run})
                evidence = root / 'evidence.json'
                report = {'version': 2, 'scope': 'internal-static-frontend-hosting', 'status': 'failed',
                    'account_id': journal.config['account_id'], 'deployment': deployment, 'site_id': SID,
                    'site_name': 'hosting-qual-test', 'project': 'test', 'environment': 'test', 'checks': {}, 'not_run': [], 'public_launch_qualified': False,
                    'custom_domain': {'hostname': journal.config['hostname'], 'run_id': RUN, 'id': DID, 'dns_cleanup': 'required'}}
                atomic_json(evidence, report)
                args = SimpleNamespace(terraform='terraform', infrastructure=root, binary='gregale', aws='aws',
                    project='test', environment='test', evidence=evidence, edge_timeout=1, cleanup_timeout=1)
                module = SimpleNamespace(Qualification=CleanupQualification, decode_json=MODULE.decode_json)
                with running(journal, cf), patch.dict(os.environ, {'FAAS_API': journal.config['api_origin']}), \
                     patch('internal_frontend_hosting_pilot.load_runner', return_value=module):
                    ProviderController(journal, cf).run('enter', scope(journal, 'domain_api'))
                    if corrupt:
                        with self.assertRaises(MODULE.Failure):
                            recovery_cleanup(journal, args)
                    else:
                        self.assertEqual(recovery_cleanup(journal, args), 0)
                        self.assertEqual(recovery_cleanup(journal, args), 0)
                    with journal.locked() as state:
                        self.assertIsNone(state['active'])
                        self.assertEqual(state['dns']['cleared'], not corrupt)
                if not corrupt:
                    saved = json.loads(private_read(evidence))
                    self.assertEqual(saved['status'], 'failed')
                    self.assertEqual(saved['pilot_recovery_cleanup'], 'passed')
                    self.assertFalse(saved['public_launch_qualified'])
                else:
                    self.assertEqual(len(cf.dns), 2)

    def test_canonical_access_file_can_refresh_without_restarting_the_runner(self):
        from test_internal_frontend_hosting_qualification import MODULE, options, FakeHTTP
        with tempfile.TemporaryDirectory(prefix='hp-') as directory:
            root = Path(directory)
            path = root / 'canonical-token'
            path.write_text('first-token')
            path.chmod(0o600)
            http = FakeHTTP()
            q = MODULE.Qualification(options(root), env={'HOSTING_QUAL_ACCESS_TOKEN_FILE': str(path)}, http=http)
            q.site, q.deployment = {'id': SID}, {'hosting_domain': 'hosting.example'}
            q.edge()
            self.assertEqual(http.calls[-1][2]['Cookie'], 'CF_Authorization=first-token')
            path.write_text('second-token')
            q.edge()
            self.assertEqual(http.calls[-1][2]['Cookie'], 'CF_Authorization=second-token')
            path.chmod(0o644)
            with self.assertRaises(MODULE.Failure):
                q.edge()


if __name__ == '__main__':
    unittest.main()
