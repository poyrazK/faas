#!/usr/bin/env python3
"""Acceptance-runner failure fixtures. No DNS, provider, or pilot credentials."""
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from test_internal_frontend_hosting_qualification import ACCOUNT, SITE, LiveFixture, MODULE, options


class AcceptanceFixture(LiveFixture):
    def __init__(self, args):
        super().__init__(args, durable=True)
        self.cap = {'account_storage_max_bytes': 1 << 30, 'account_pending_uploads_max': 4,
                    'release_max_bytes': 100 << 20, 'file_max_bytes': 32 << 20,
                    'manifest_max_bytes': 512 << 10, 'releases_per_site': 10,
                    'analytics_enabled': True, 'domains_enabled': True, 'domain_cname_target': 'customers.hosting.example'}
        self.domain_row = None
        self.domain_removed = False
        self.domain_calls = []
        self.domain_synced = False
        self.releases_created = 0
        self.traffic_value = self.traffic()
        self.traffic_status = 200
        self.http.request = self.http_request

    def preflight(self):
        super().preflight()
        self.deployment.update(analytics_configured=True, analytics_enabled=True, custom_domains_configured=True,
                               custom_domains_enabled=True, domain_cname_target=self.cap['domain_cname_target'])
        self.prepare_acceptance()

    def new_release(self, manifest):
        self.releases_created += 1
        rid = f'33333333-3333-4333-8333-{self.releases_created:012d}'
        row = {'id': rid, 'site_id': SITE, 'state': 'pending', 'manifest': deepcopy(manifest),
               'artifact_bytes': sum(f['size'] for f in manifest['files'])}
        self.releases[rid] = row
        return row

    def usage_value(self):
        # A separate cleanup tick follows the retirement observation, keeping
        # admission and retirement independently visible to the runner.
        if 'usage_quota_retiring' in self.report['checks'] and 'usage_quota_reclaimed' not in self.report['checks']:
            self.releases = {rid: r for rid, r in self.releases.items() if r['state'] != 'retiring'}
        rows = [] if self.deleted else list(self.releases.values())
        return {'account_id': ACCOUNT, 'storage_limit_bytes': self.cap['account_storage_max_bytes'],
                'pending_uploads_limit': self.cap['account_pending_uploads_max'],
                'reserved_bytes': sum(2 * r['artifact_bytes'] + (512 << 10) for r in rows),
                'payload_bytes': sum(r['artifact_bytes'] for r in rows),
                'ready_payload_bytes': sum(r['artifact_bytes'] for r in rows if r['state'] == 'ready'),
                'retiring_reserved_bytes': sum(2 * r['artifact_bytes'] + (512 << 10) for r in rows if r['state'] == 'retiring'),
                'pending_uploads': sum(r['state'] == 'pending' for r in rows), 'releases': len(rows)}

    def api_response(self, method, suffix='', value=None, **kwargs):
        def response(status, value):
            return status, {'cache-control': 'no-store'}, json.dumps(value).encode()
        if kwargs.get('root') == '/v1/internal/hosting':
            return response(200, self.cap if suffix == '/capabilities' else self.usage_value())
        if suffix.endswith('/traffic'):
            return response(self.traffic_status, self.traffic_value)
        if suffix == '/' + SITE + '/releases' and method == 'POST':
            usage = self.usage_value()
            size = sum(f['size'] for f in value['files'])
            storage = usage['reserved_bytes'] + 2 * size + (512 << 10)
            if storage > self.cap['account_storage_max_bytes']:
                return response(409, {'code': 'hosting_storage_quota_exceeded', 'limit': self.cap['account_storage_max_bytes'],
                                      'observed': storage, 'docs_url': 'https://docs.example/hosting'})
            if usage['pending_uploads'] >= self.cap['account_pending_uploads_max']:
                return response(409, {'code': 'hosting_pending_upload_quota_exceeded', 'limit': self.cap['account_pending_uploads_max'],
                                      'observed': usage['pending_uploads'] + 1, 'docs_url': 'https://docs.example/hosting'})
        if '/domains' in suffix:
            if method == 'POST':
                self.domain_row = {'id': '44444444-4444-4444-8444-444444444444', 'site_id': SITE,
                                   'account_id': ACCOUNT, 'hostname': self.args.custom_domain, 'revision': 1, 'provider_origin_id': 'e' * 64,
                                   'challenge': 'c' * 64, 'state': 'pending_dns', 'generation': 1, 'published_generation': 0,
                                   'details': {'records': [], 'tls_status': 'pending'}}
                return response(202, self.domain_row)
            if method == 'DELETE':
                if value['expected_revision'] != self.domain_row['revision']:
                    return response(409, {'code': 'hosting_domain_conflict'})
                self.domain_removed = True
                return response(202, self.domain_row)
            if not self.domain_row or self.domain_removed or self.deleted:
                return response(200, [])
            if self.domain_synced:
                self.domain_row.update(state='active', generation=2, published_generation=2)
                self.domain_row['details'].update(tls_status='active', access_aud='d' * 64)
            return response(200, [self.domain_row])
        return super().api_response(method, suffix, value, **kwargs)

    def domain_control(self, action, records=None):
        self.domain_calls.append((action, deepcopy(records)))
        if action == 'sync':
            self.domain_synced = True
        return {'dedicated': True, 'disposable_hostname': True, 'applied': True,
                'cleared': True, 'access_token': 'SECRET-DOMAIN'}

    def http_request(self, method, url, headers=None, body=None, timeout=30):
        if url.startswith('https://' + self.args.custom_domain + '/') and self.args.custom_domain:
            if self.domain_removed or self.deleted:
                return 503, {}, b'{}'
            return self.edge(url.split(self.args.custom_domain, 1)[1], method=method,
                             authenticated=(headers or {}).get('Cookie') == 'CF_Authorization=SECRET-DOMAIN', headers=headers)
        return 403, {}, b'private'

    def storage(self, *args):
        head = {'version': 1, 'deleted': self.domain_removed or self.deleted, 'enabled': False,
                'id': self.domain_row['id'], 'site_id': SITE, 'hostname': self.args.custom_domain,
                'canonical_hostname': self.site['hostname'], 'origin_id': self.site['origin_id'], 'generation': 3}
        Path(args[-1]).write_text(json.dumps(head))
        return {}

    @staticmethod
    def traffic():
        from internal_frontend_hosting_acceptance import COUNTERS
        now = datetime.now(timezone.utc)
        end = now.replace(second=0, microsecond=0)
        start = end - timedelta(hours=24)
        at = start.replace(minute=0)
        buckets = []
        while at < end:
            buckets.append({'start': at.isoformat(), **dict.fromkeys(COUNTERS, 0)})
            at += timedelta(hours=1)
        buckets[-1].update(requests=5, payload_bytes=100, cache_hits=2, cache_misses=1, client_errors=1, server_errors=1)
        return {'site_id': SITE, 'source': 'cloudflare_analytics_engine', 'approximate': True,
                'period_start': start.isoformat(), 'period_end': end.isoformat(), 'observed_at': now.isoformat(),
                'max_sample_interval': 3, **{k: buckets[-1][k] for k in COUNTERS}, 'buckets': buckets,
                'failures': [{'start': buckets[-1]['start'], 'status': 503, 'code': 'hosting_artifact_unavailable', 'requests': 1}]}


def acceptance_options(root, *, usage=True, traffic=True, domain=True):
    args = options(root)
    args.check_usage, args.check_traffic = usage, traffic
    args.custom_domain = 'pilot.customer.example' if domain else ''
    args.domain_controller = root / 'controller' if domain else None
    if domain:
        args.domain_controller.write_text('#!/bin/sh\nexit 1\n')
        args.domain_controller.chmod(0o700)
    return args


class HostingAcceptanceTests(unittest.TestCase):
    def run_fixture(self, root, **kwargs):
        fixture = AcceptanceFixture(acceptance_options(root, **kwargs))
        return fixture

    def test_complete_fixture_qualifies_observations_but_preserves_live_gates(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory))
            self.assertTrue(q.run(), q.report.get('failure'))
            for name in ('account_quota_admission', 'account_reservation_cleanup', 'traffic_observations',
                         'custom_domain_lifecycle', 'access_cold_warm_head_conditional', 'custom_domain_promotion',
                         'custom_domain_rollback', 'custom_domain_suspended'):
                self.assertIn(name, q.report['checks'])
                self.assertNotIn(name, q.report['not_run'])
            for name in ('certificate_renewal', 'custom_domain_ambiguous_create_recovery',
                         'custom_domain_provider_outage', 'traffic_provider_outage', 'provider_usage_billing'):
                self.assertIn(name, q.report['not_run'])
            self.assertFalse(q.report['public_launch_qualified'])
            self.assertEqual(q.report['version'], 2)
            self.assertEqual(q.report['checks']['account_quota_admission']['uploaded_bytes'], 0)
            self.assertEqual(q.report['checks']['account_quota_admission']['retained_releases'], 5)
            self.assertEqual(q.report['checks']['usage_after_finalization']['pending_uploads'], 0)
            self.assertEqual(q.report['custom_domain']['dns_cleanup'], 'passed')
            self.assertEqual(q.domain_calls[-1][0], 'clear')
            self.assertNotIn('SECRET', q.args.evidence.read_text())

    def test_new_checks_remain_opt_in(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, traffic=False, domain=False)
            self.assertTrue(q.run())
            for name in ('account_quota_admission', 'account_reservation_cleanup', 'traffic_observations', 'custom_domain_lifecycle'):
                self.assertIn(name, q.report['not_run'])
            self.assertEqual(q.domain_calls, [])

    def test_extended_scenarios_compose_with_existing_outages_and_regional_probes(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            args = acceptance_options(Path(directory))
            args.outage_controller = args.domain_controller
            args.check_control_plane_outages = args.check_publication_recovery = args.resource_profile = True
            args.edge_samples, args.runner_region = 3, 'eu-fixture'
            q = AcceptanceFixture(args)
            self.assertTrue(q.run(), q.report.get('failure'))
            for check in ('account_quota_admission', 'traffic_observations', 'custom_domain_lifecycle',
                          'api_control_plane_outage', 'database_control_plane_outage', 'publication_retry_recovery',
                          'regional_edge_observations', 'control_plane_resource_profile'):
                self.assertIn(check, q.report['checks'])
            self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_quota_checks_require_empty_account_before_site_creation(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), traffic=False, domain=False)
            q.new_release(MODULE.manifest({'index.html': b'other account work'}))
            self.assertFalse(q.run())
            self.assertIsNone(q.site)
            self.assertIn('empty and exclusively owned', q.report['failure'])

    def test_retirement_cannot_release_storage_early(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), traffic=False, domain=False)
            original = q.usage_value
            def undercount():
                value = original()
                if any(r['state'] == 'retiring' for r in q.releases.values()) and not q.deleted:
                    value['reserved_bytes'] = 0
                return value
            q.usage_value = undercount
            self.assertFalse(q.run())
            self.assertNotIn('account_quota_admission', q.report['checks'])
            self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_traffic_empty_unavailable_foreign_or_inconsistent_cannot_qualify(self):
        for mode in ('empty', 'outage', 'foreign', 'bucket_sum', 'sampling', 'unknown_failure', 'stale'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = self.run_fixture(Path(directory), usage=False, domain=False)
                if mode == 'empty':
                    from internal_frontend_hosting_acceptance import COUNTERS
                    for row in [q.traffic_value, *q.traffic_value['buckets']]:
                        row.update(dict.fromkeys(COUNTERS, 0))
                    q.traffic_value['failures'] = []
                    q.traffic_value['max_sample_interval'] = 0
                    # Exercise the real empty-result retry, with an immediate fixture deadline.
                    q.args.edge_timeout = .00001
                elif mode == 'outage':
                    q.traffic_status = 503
                    q.traffic_value = {'code': 'hosting_traffic_unavailable', 'detail': 'SECRET-PROVIDER'}
                elif mode == 'foreign':
                    q.traffic_value['site_id'] = ACCOUNT
                elif mode == 'bucket_sum':
                    q.traffic_value['requests'] += 1
                elif mode == 'sampling':
                    q.traffic_value['max_sample_interval'] = True
                elif mode == 'unknown_failure':
                    q.traffic_value['failures'][0]['code'] = 'SECRET-UPSTREAM'
                else:
                    q.traffic_value['observed_at'] = '2000-01-01T00:00:00Z'
                self.assertFalse(q.run())
                self.assertNotIn('traffic_observations', q.report['checks'])
                self.assertIn('traffic_observations', q.report['not_run'])
                self.assertEqual(q.report['cleanup']['status'], 'passed')
                self.assertNotIn('SECRET', q.args.evidence.read_text())

    def test_domain_wrong_audience_is_rejected_even_after_cache_hit(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, traffic=False)
            original = q.http_request
            def bypass(method, url, headers=None, **kwargs):
                if (headers or {}).get('Cookie') == 'CF_Authorization=SECRET-ACCESS' and q.args.custom_domain in url:
                    return q.edge('/', headers=headers)
                return original(method, url, headers, **kwargs)
            q.http.request = bypass
            self.assertFalse(q.run())
            self.assertNotIn('custom_domain_lifecycle', q.report['checks'])
            self.assertEqual(q.report['cleanup']['status'], 'passed')
            self.assertEqual(q.domain_calls[-1][0], 'clear')

    def test_untrusted_dns_records_are_never_sent_to_controller(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, traffic=False)
            original = q.create_domain
            def create():
                original()
                q.domain_row['details']['records'] = [{'type': 'TXT', 'name': 'production.example', 'value': 'SECRET'}]
            q.create_domain = create
            self.assertFalse(q.run())
            self.assertFalse(any(action == 'sync' for action, _ in q.domain_calls))
            self.assertNotIn('SECRET', q.args.evidence.read_text())

    def test_ambiguous_provider_create_requires_manual_recovery_and_preserves_dns(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, traffic=False)
            original = q.create_domain
            def conflict():
                original()
                q.domain_row['last_error_code'] = 'provider_conflict'
            q.create_domain = conflict
            # Unbound uncertain provider state blocks site purge in production.
            original_api = q.api_response
            def retained(method, suffix='', value=None, **kwargs):
                if q.deleted and suffix == '/' + SITE:
                    return 200, {}, json.dumps(q.live_site).encode()
                return original_api(method, suffix, value, **kwargs)
            q.api_response = retained
            q.args.cleanup_timeout = .00001
            self.assertFalse(q.run())
            self.assertIn('explicit reviewed provider-ID recovery', q.report['failure'])
            self.assertEqual(q.report['cleanup']['status'], 'failed')
            self.assertEqual(q.report['custom_domain']['dns_cleanup'], 'required')
            self.assertFalse(any(action == 'clear' for action, _ in q.domain_calls))

    def test_lost_dns_write_response_still_cleans_after_site_provider_cleanup(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, traffic=False)
            original = q.domain_control
            def lose(action, records=None):
                result = original(action, records)
                if action == 'sync':
                    raise MODULE.Failure('Qualification command unavailable or timed out')
                return result
            q.domain_control = lose
            self.assertFalse(q.run())
            self.assertEqual(q.report['cleanup']['status'], 'passed')
            self.assertEqual(q.report['custom_domain']['dns_cleanup'], 'passed')

    def test_terminal_head_is_required_before_dns_removal(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, traffic=False)
            def missing(*args):
                Path(args[-1]).write_text('{}')
            q.storage = missing
            self.assertFalse(q.run())
            self.assertEqual(q.report['cleanup']['status'], 'failed')
            self.assertFalse(any(action == 'clear' for action, _ in q.domain_calls))
            self.assertEqual(q.report['custom_domain']['dns_cleanup'], 'required')

    def test_controller_has_no_runner_credentials_and_checks_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            args = acceptance_options(Path(directory), usage=False, traffic=False)
            q = MODULE.Qualification(args, env={'FAAS_TOKEN': 'SECRET-API', 'HOSTING_QUAL_ACCESS_TOKEN': 'SECRET-ACCESS',
                                              'AWS_SECRET_ACCESS_KEY': 'SECRET-AWS', 'SSH_AUTH_SOCK': '/run/agent'})
            q.api_url = 'https://api.platform.example'
            from test_internal_frontend_hosting_qualification import deployment
            q.deployment = deployment()
            calls = []
            def execute(command, env, **kwargs):
                request = json.loads(kwargs['input'])
                calls.append((command, env, request))
                return type('Result', (), {'stdout': json.dumps(request).encode()})()
            q.probe_execute = execute
            q.domain_control('describe')
            self.assertEqual(calls[0][1], {'SSH_AUTH_SOCK': '/run/agent'})
            self.assertNotIn('SECRET', json.dumps(calls))
            def foreign(command, env, **kwargs):
                request = json.loads(kwargs['input'])
                request['hostname'] = 'production.example'
                return type('Result', (), {'stdout': json.dumps(request).encode()})()
            q.probe_execute = foreign
            with self.assertRaisesRegex(MODULE.Failure, 'identity differs'):
                q.domain_control('sync')

    def test_optional_flags_fail_before_creating_site_when_not_configured(self):
        for mode in ('traffic', 'domains', 'controller', 'domain_overlap'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = self.run_fixture(Path(directory), usage=False)
                if mode == 'traffic':
                    q.cap['analytics_enabled'] = False
                elif mode == 'domains':
                    q.cap['domains_enabled'] = False
                elif mode == 'controller':
                    q.args.domain_controller = None
                else:
                    q.args.custom_domain = 'api.platform.example'
                self.assertFalse(q.run())
                self.assertIsNone(q.site)

    def test_head_and_conditional_access_bypasses_fail_before_publication_checks(self):
        for method in ('HEAD', 'conditional'):
            with self.subTest(method=method), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = self.run_fixture(Path(directory), usage=False, traffic=False, domain=False)
                original = q.edge
                def bypass(path='/', **kwargs):
                    target = kwargs.get('method') == 'HEAD' if method == 'HEAD' else bool(kwargs.get('headers', {}).get('If-None-Match'))
                    if target and kwargs.get('authenticated') is False:
                        return 200 if method == 'HEAD' else 304, {}, b''
                    return original(path, **kwargs)
                q.edge = bypass
                self.assertFalse(q.run())
                self.assertNotIn('access_cold_warm_head_conditional', q.report['checks'])
                self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_quota_rejection_must_report_attempted_value(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), traffic=False, domain=False)
            original = q.api_response
            def incorrect(*args, **kwargs):
                status, headers, body = original(*args, **kwargs)
                if status == 409 and b'quota_exceeded' in body:
                    problem = json.loads(body)
                    problem['observed'] = problem['limit']
                    body = json.dumps(problem).encode()
                return status, headers, body
            q.api_response = incorrect
            self.assertFalse(q.run())
            self.assertNotIn('account_quota_admission', q.report['checks'])
            self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_non_atomic_account_snapshot_retries_before_qualifying(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), traffic=False, domain=False)
            original = q.usage_value
            changed = False
            def racing():
                nonlocal changed
                value = original()
                if 'immutable_publication_and_retry' in q.report['checks'] and not changed:
                    changed = True
                    value['payload_bytes'] += 1
                return value
            q.usage_value = racing
            self.assertTrue(q.run(), q.report.get('failure'))
            self.assertTrue(changed)
            self.assertIn('usage_after_finalization', q.report['checks'])

    def test_empty_traffic_is_retried_without_qualifying_zero_as_health(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, domain=False)
            original = q.api_response
            reads = 0
            def empty_then_observed(method, suffix='', *args, **kwargs):
                nonlocal reads
                status, headers, body = original(method, suffix, *args, **kwargs)
                if suffix.endswith('/traffic'):
                    reads += 1
                    if reads == 1:
                        from internal_frontend_hosting_acceptance import COUNTERS
                        value = json.loads(body)
                        for row in [value, *value['buckets']]:
                            row.update(dict.fromkeys(COUNTERS, 0))
                        value.update(failures=[], max_sample_interval=0)
                        body = json.dumps(value).encode()
                return status, headers, body
            q.api_response = empty_then_observed
            self.assertTrue(q.run(), q.report.get('failure'))
            self.assertEqual(reads, 2)
            self.assertEqual(q.report['checks']['traffic_observations']['requests'], 5)

    def test_cached_domain_content_must_stop_serving_before_cleanup_qualifies(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, traffic=False)
            original = q.http_request
            def stale(method, url, headers=None, **kwargs):
                if q.domain_removed and not q.deleted and q.args.custom_domain in url:
                    return q.edge('/', headers=headers)
                return original(method, url, headers, **kwargs)
            q.http.request = stale
            q.args.edge_timeout = .00001
            self.assertFalse(q.run())
            self.assertNotIn('custom_domain_cached_content_revoked', q.report['checks'])
            self.assertNotIn('custom_domain_lifecycle', q.report['checks'])
            self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_lost_dns_clear_response_leaves_explicit_cleanup_work(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = self.run_fixture(Path(directory), usage=False, traffic=False)
            original = q.domain_control
            def lost(action, records=None):
                result = original(action, records)
                if action == 'clear':
                    raise MODULE.Failure('Qualification command unavailable or timed out')
                return result
            q.domain_control = lost
            self.assertFalse(q.run())
            self.assertEqual(q.report['cleanup']['status'], 'failed')
            self.assertEqual(q.report['custom_domain']['dns_cleanup'], 'required')
            self.assertNotIn('custom_domain_lifecycle', q.report['checks'])
            self.assertEqual([action for action, _ in q.domain_calls].count('clear'), 2)


if __name__ == '__main__':
    unittest.main()
