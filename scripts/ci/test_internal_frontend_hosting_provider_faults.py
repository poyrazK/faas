#!/usr/bin/env python3
"""Provider-failure acceptance fixtures with independent API, controller and review boundaries."""
from copy import deepcopy
from datetime import datetime, timezone
import json
from pathlib import Path
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from test_internal_frontend_hosting_acceptance import AcceptanceFixture, acceptance_options
from test_internal_frontend_hosting_qualification import ACCOUNT, SITE, MODULE

PROVIDER_ID = '55555555-5555-4555-8555-555555555555'


def provider_options(root, *, recovery=True, domain_outage=True, analytics=True, domain=True):
    args = acceptance_options(root, usage=False, traffic=analytics, domain=domain)
    args.provider_controller = root / 'provider-controller'
    args.provider_controller.write_text('#!/bin/sh\nexit 1\n')
    args.provider_controller.chmod(0o700)
    args.check_domain_recovery = recovery
    args.check_domain_provider_outage = domain_outage
    args.check_analytics_outage = analytics
    args.domain_recovery_review = root / 'review.json' if recovery else None
    args.provider_timeout, args.provider_observation_seconds = 2400, 180
    args.edge_timeout = 240
    return args


class ProviderFixture(AcceptanceFixture):
    """A clock-driven reconciler, successful-but-lost create, and query-cache expiry."""
    def __init__(self, args):
        super().__init__(args)
        self.running_fault = None
        self.journals = {}
        self.provider_calls = []
        self.create_requests = self.provider_objects = self.dropped_responses = 0
        self.next_domain_poll = 0
        self.create_started = None
        self.cache_expires = 0
        self.recovery_posts = 0
        self.auto_review = True
        self.lost_recovery = False
        self.probe_execute = self.controller_execute

    def preflight(self):
        super().preflight()
        self.prepare_provider_faults()

    def wait(self, name, check, timeout=None):
        started = self.elapsed
        while True:
            result = check()
            if result:
                return result, self.elapsed - started
            MODULE.require(self.elapsed - started < (timeout or self.args.edge_timeout), 'Timed out waiting for ' + name)
            self.probe_sleep(2)

    def controller_execute(self, command, env, **kwargs):
        scope = json.loads(kwargs['input'])
        action = command[1]
        self.provider_calls.append((action, deepcopy(scope), deepcopy(env)))
        value = deepcopy(scope)
        if action == 'describe':
            value.update(dedicated=True, modes=['create_response', 'domain_api', 'analytics_query'])
        else:
            run = scope['run_id']
            if action == 'enter':
                MODULE.require(self.running_fault is None, 'Fixture controller rejected overlap')
                self.running_fault = run
                self.journals[run] = {'scope': deepcopy(scope), 'failed_requests': 0, 'successful_requests_after_restore': 0}
                value['automatic_restore_at'] = time.time() + scope['duration'] + 120
            MODULE.require(self.journals[run]['scope'] == scope, 'Fixture controller rejected a foreign run')
            if action == 'leave':
                if self.running_fault == run:
                    self.running_fault = None
            self.reconcile()
            value.update(fault_active=self.running_fault == run, api_running=True,
                         create_requests=self.create_requests, provider_objects=self.provider_objects,
                         dropped_responses=self.dropped_responses,
                         **{k: self.journals[run][k] for k in ('failed_requests', 'successful_requests_after_restore')})
        return SimpleNamespace(stdout=json.dumps(value).encode())

    def mode(self):
        return self.journals[self.running_fault]['scope']['mode'] if self.running_fault else ''

    def reconcile(self):
        if not self.domain_row or not self.domain_synced or self.domain_removed or self.deleted or self.elapsed < self.next_domain_poll:
            return
        row, details = self.domain_row, self.domain_row['details']
        row['attempts'] = row.get('attempts', 0) + 1
        self.next_domain_poll = self.elapsed + 60
        if not details.get('verified_at'):
            details['verified_at'] = datetime.now(timezone.utc).isoformat()
        if not details.get('provider_id'):
            if details.get('create_started_at'):
                row['last_error_code'] = 'provider_conflict'
                return  # A reconciler retry must never issue a second create.
            details['create_started_at'] = datetime.now(timezone.utc).isoformat()
            self.create_started = self.elapsed
            self.create_requests += 1
            self.provider_objects += 1
            if self.mode() == 'create_response':
                self.dropped_responses += 1
                row.update(state='pending_certificate', last_error_code='provider_unavailable')
                return
            details['provider_id'] = PROVIDER_ID
        if self.mode() == 'domain_api':
            self.journals[self.running_fault]['failed_requests'] += 1
            row['last_error_code'] = 'provider_unavailable'
            return
        row.update(state='active', generation=2, published_generation=2, last_error_code='')
        details.update(tls_status='active', access_aud='d' * 64, access_id='66666666-6666-4666-8666-666666666666')
        self.next_domain_poll = self.elapsed + 1800
        for journal in self.journals.values():
            if journal['scope']['mode'] == 'domain_api' and self.running_fault != journal['scope']['run_id']:
                journal['successful_requests_after_restore'] += 1

    def api_response(self, method, suffix='', value=None, **kwargs):
        def response(status, value):
            return status, {'cache-control': 'no-store'}, json.dumps(value).encode()
        if method == 'GET' and suffix == '/' + SITE and self.deleted and self.domain_row and self.provider_objects and not self.domain_row['details'].get('provider_id'):
            return response(200, self.live_site)  # Unknown provider ownership prevents safe purge.
        if suffix.endswith('/traffic'):
            if self.elapsed >= self.cache_expires:
                if self.mode() == 'analytics_query':
                    self.journals[self.running_fault]['failed_requests'] += 1
                    return response(503, {'code': 'hosting_traffic_unavailable', 'detail': 'SECRET-PROVIDER'})
                self.cache_expires = self.elapsed + 60
                for journal in self.journals.values():
                    if journal['scope']['mode'] == 'analytics_query' and self.running_fault != journal['scope']['run_id']:
                        journal['successful_requests_after_restore'] += 1
            return response(200, self.traffic_value)
        if '/domains' in suffix and method == 'GET':
            self.reconcile()
            return response(200, [] if self.domain_removed or self.deleted else [self.domain_row])
        if suffix.endswith('/recover'):
            self.recovery_posts += 1
            row = self.domain_row
            if value['provider_id'] != PROVIDER_ID or value['expected_revision'] != row['revision'] or \
                    value['expected_hostname'] != row['hostname'] or row['details'].get('provider_id') or \
                    self.elapsed - self.create_started < 160:
                return response(409, {'code': 'hosting_domain_conflict'})
            row['details']['provider_id'] = value['provider_id']
            row['revision'] += 1
            row['last_error_code'] = ''
            self.next_domain_poll = self.elapsed
            if self.lost_recovery:
                raise MODULE.Failure('HTTPS request failed; verify connectivity and credentials separately')
            return response(202, row)
        return super().api_response(method, suffix, value, **kwargs)

    def reviewed_provider(self, scope):
        if self.auto_review:
            self.args.domain_recovery_review.write_text(json.dumps({**scope, 'provider_id': PROVIDER_ID}))
        return super().reviewed_provider(scope)


def fixture(root, **kwargs):
    return ProviderFixture(provider_options(root, **kwargs))


class ProviderFaultTests(unittest.TestCase):
    def test_complete_failure_recovery_run_requires_observed_faults_and_preserves_serving(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = fixture(Path(directory))
            self.assertTrue(q.run(), q.report.get('failure'))
            for name in ('custom_domain_ambiguous_create_recovery', 'custom_domain_provider_outage', 'traffic_provider_outage'):
                self.assertIn(name, q.report['checks'])
                self.assertNotIn(name, q.report['not_run'])
                self.assertEqual(q.report['checks'][name]['observed_seconds'], 180)
            self.assertEqual(q.create_requests, 1)
            self.assertEqual(q.recovery_posts, 1)
            self.assertEqual(q.report['domain_recovery_review']['status'], 'completed')
            self.assertEqual(q.report['checks']['custom_domain_provider_outage']['cold_samples'], 38)
            self.assertEqual(len(q.report['provider_faults']), 3)
            self.assertTrue(all(f['status'] == 'restored' for f in q.report['provider_faults']))
            self.assertIn('certificate_renewal', q.report['not_run'])
            self.assertFalse(q.report['public_launch_qualified'])
            self.assertNotIn('SECRET', q.args.evidence.read_text())
            self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_analytics_outage_can_run_without_custom_domains(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = fixture(Path(directory), recovery=False, domain_outage=False, domain=False)
            self.assertTrue(q.run(), q.report.get('failure'))
            self.assertIn('traffic_provider_outage', q.report['checks'])
            self.assertIn('custom_domain_provider_outage', q.report['not_run'])
            self.assertEqual(q.report['checks']['traffic_provider_outage']['cold_samples'], 19)

    def test_provider_checks_remain_off_without_explicit_scenario_flags(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = fixture(Path(directory), recovery=False, domain_outage=False, analytics=False, domain=False)
            self.assertTrue(q.run(), q.report.get('failure'))
            self.assertEqual(q.provider_calls, [])
            self.assertNotIn('provider_faults', q.report)
            for name in ('custom_domain_ambiguous_create_recovery', 'custom_domain_provider_outage', 'traffic_provider_outage'):
                self.assertIn(name, q.report['not_run'])

    def test_all_scenarios_compose_with_quota_outages_regions_and_resource_profile(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = fixture(Path(directory))
            q.args.check_usage = True
            q.args.outage_controller = q.args.provider_controller
            q.args.check_control_plane_outages = q.args.check_publication_recovery = q.args.resource_profile = True
            q.args.edge_samples, q.args.runner_region = 3, 'eu-fixture'
            self.assertTrue(q.run(), q.report.get('failure'))
            for name in ('account_quota_admission', 'account_reservation_cleanup', 'api_control_plane_outage',
                         'database_control_plane_outage', 'publication_retry_recovery', 'regional_edge_observations',
                         'control_plane_resource_profile', 'custom_domain_ambiguous_create_recovery',
                         'custom_domain_provider_outage', 'traffic_provider_outage'):
                self.assertIn(name, q.report['checks'])
            self.assertEqual(q.report['cleanup']['status'], 'passed')
            self.assertEqual(q.create_requests, 1)

    def test_lost_recovery_response_observes_receipt_without_reposting(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = fixture(Path(directory), domain_outage=False, analytics=False)
            q.lost_recovery = True
            self.assertTrue(q.run(), q.report.get('failure'))
            self.assertEqual(q.recovery_posts, 1)
            self.assertEqual(q.create_requests, 1)

    def test_missing_or_invalid_review_never_binds_a_provider(self):
        for mode in ('absent', 'foreign_run', 'stale_revision', 'foreign_domain', 'wrong_provider', 'malformed', 'symlink'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = fixture(Path(directory), domain_outage=False, analytics=False)
                q.auto_review = False
                original = q.reviewed_provider
                def review(scope):
                    value = {**scope, 'provider_id': PROVIDER_ID}
                    if mode == 'foreign_run':
                        value['run_id'] = ACCOUNT
                    if mode == 'stale_revision':
                        value['expected_revision'] += 1
                    if mode == 'foreign_domain':
                        value['hostname'] = 'production.example'
                    if mode == 'wrong_provider':
                        value['provider_id'] = ACCOUNT
                    if mode not in ('absent', 'symlink'):
                        q.args.domain_recovery_review.write_text('SECRET' if mode == 'malformed' else json.dumps(value))
                    if mode == 'symlink' and not q.args.domain_recovery_review.is_symlink():
                        target = Path(directory) / 'target.json'
                        target.write_text(json.dumps(value))
                        q.args.domain_recovery_review.symlink_to(target)
                    return original(scope)
                q.reviewed_provider = review
                self.assertFalse(q.run())
                self.assertNotIn('custom_domain_ambiguous_create_recovery', q.report['checks'])
                self.assertEqual(q.recovery_posts, 1 if mode == 'wrong_provider' else 0)
                self.assertEqual(q.create_requests, 1)
                self.assertEqual(q.report['custom_domain']['dns_cleanup'], 'required')
                self.assertEqual(q.report['provider_fault']['status'], 'restored')
                self.assertNotIn('SECRET', q.args.evidence.read_text())

    def test_duplicate_create_or_implicit_adoption_cannot_qualify(self):
        for mode in ('duplicate', 'adopt', 'no_retry'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = fixture(Path(directory), domain_outage=False, analytics=False)
                original = q.reconcile
                def broken():
                    old_attempts = (q.domain_row or {}).get('attempts', 0)
                    original()
                    if q.mode() == 'create_response' and q.domain_row and q.domain_row.get('last_error_code') == 'provider_conflict':
                        if mode == 'duplicate':
                            q.create_requests = 2
                        elif mode == 'adopt':
                            q.domain_row['details']['provider_id'] = PROVIDER_ID
                        else:
                            q.domain_row['attempts'] = min(old_attempts, 2)
                q.reconcile = broken
                self.assertFalse(q.run())
                self.assertEqual(q.recovery_posts, 0)
                self.assertNotIn('custom_domain_ambiguous_create_recovery', q.report['checks'])

    def test_fault_watchdog_identity_and_actual_failure_are_required(self):
        for mode in ('watchdog', 'foreign_scope', 'inactive', 'api_down', 'no_failed_request'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = fixture(Path(directory), recovery=False, domain_outage=False, domain=False)
                original = q.controller_execute
                def invalid(command, env, **kwargs):
                    result = original(command, env, **kwargs)
                    value = json.loads(result.stdout)
                    if command[1] == 'enter' and mode == 'watchdog':
                        value.pop('automatic_restore_at')
                    if command[1] == 'status':
                        if mode == 'foreign_scope':
                            value['site_id'] = ACCOUNT
                        elif mode == 'inactive':
                            value['fault_active'] = False
                        elif mode == 'api_down':
                            value['api_running'] = False
                        elif mode == 'no_failed_request':
                            value['failed_requests'] = 0
                    return SimpleNamespace(stdout=json.dumps(value).encode())
                q.probe_execute = invalid
                self.assertFalse(q.run())
                self.assertNotIn('traffic_provider_outage', q.report['checks'])
                self.assertEqual(q.report['provider_fault']['status'], 'restored')
                self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_lost_enter_and_leave_responses_restore_idempotently(self):
        for action in ('enter', 'leave'):
            with self.subTest(action=action), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = fixture(Path(directory), recovery=False, domain_outage=False, domain=False)
                original = q.controller_execute
                lost = False
                def lose(command, env, **kwargs):
                    nonlocal lost
                    result = original(command, env, **kwargs)
                    if command[1] == action and not lost:
                        lost = True
                        raise MODULE.Failure('Qualification command unavailable or timed out')
                    return result
                q.probe_execute = lose
                self.assertFalse(q.run())
                self.assertIsNone(q.running_fault)
                self.assertIsNone(q.provider_fault)
                self.assertEqual(q.report['cleanup']['status'], 'passed')
                self.assertNotIn('traffic_provider_outage', q.report['checks'])
                self.assertGreaterEqual(sum(c[0] == 'leave' for c in q.provider_calls), 2 if action == 'leave' else 1)

    def test_unrestored_fault_blocks_cleanup_and_dns_removal(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = fixture(Path(directory), recovery=False, domain_outage=False)
            original = q.controller_execute
            def broken(command, env, **kwargs):
                if command[1] == 'leave':
                    raise MODULE.Failure('Qualification command unavailable or timed out')
                return original(command, env, **kwargs)
            q.probe_execute = broken
            self.assertFalse(q.run())
            self.assertEqual(q.report['provider_fault']['status'], 'restore_failed')
            self.assertEqual(q.report['cleanup']['status'], 'blocked')
            self.assertFalse(q.deleted)
            self.assertEqual(q.report['custom_domain']['dns_cleanup'], 'required')
            self.assertFalse(any(action == 'clear' for action, _ in q.domain_calls))

    def test_analytics_cache_flag_errors_and_zero_recovery_do_not_qualify(self):
        for mode in ('forever_cached', 'flag_disabled', 'zero_recovery', 'no_success_receipt'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = fixture(Path(directory), recovery=False, domain_outage=False, domain=False)
                original = q.api_response
                def invalid(method, suffix='', value=None, **kwargs):
                    if suffix.endswith('/traffic'):
                        if q.mode() == 'analytics_query' and mode == 'forever_cached':
                            q.cache_expires = q.elapsed + 60
                        if q.mode() == 'analytics_query' and mode == 'flag_disabled':
                            return 503, {'cache-control': 'no-store'}, b'{"code":"frontend_hosting_analytics_disabled"}'
                        if not q.running_fault and q.journals and mode == 'zero_recovery':
                            from internal_frontend_hosting_acceptance import COUNTERS
                            value = deepcopy(q.traffic_value)
                            for row in [value, *value['buckets']]:
                                row.update(dict.fromkeys(COUNTERS, 0))
                            value.update(failures=[], max_sample_interval=0)
                            return 200, {'cache-control': 'no-store'}, json.dumps(value).encode()
                    return original(method, suffix, value, **kwargs)
                q.api_response = invalid
                if mode == 'no_success_receipt':
                    execute = q.controller_execute
                    def no_success(command, env, **kwargs):
                        result = execute(command, env, **kwargs)
                        value = json.loads(result.stdout)
                        value['successful_requests_after_restore'] = 0
                        return SimpleNamespace(stdout=json.dumps(value).encode())
                    q.probe_execute = no_success
                self.assertFalse(q.run())
                self.assertNotIn('traffic_provider_outage', q.report['checks'])
                self.assertEqual(q.report['provider_fault']['status'], 'restored')
                self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_domain_outage_must_be_real_and_keep_the_bound_alias(self):
        for mode in ('no_api_error', 'unbind', 'unpublish', 'serve_failure', 'access_bypass', 'no_recovery'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = fixture(Path(directory), recovery=False, analytics=False)
                original = q.reconcile
                def broken():
                    original()
                    row = q.domain_row
                    if row and q.mode() == 'domain_api':
                        if mode == 'no_api_error':
                            row['last_error_code'] = ''
                        elif mode == 'unbind':
                            row['details']['provider_id'] = ACCOUNT
                        elif mode == 'unpublish':
                            row['published_generation'] = 0
                    if row and not q.running_fault and q.journals and mode == 'no_recovery':
                        row['last_error_code'] = 'provider_unavailable'
                q.reconcile = broken
                if mode == 'serve_failure':
                    edge = q.domain_edge
                    q.domain_edge = lambda *a, **kw: (503, {}, b'{}') if q.mode() == 'domain_api' else edge(*a, **kw)
                if mode == 'access_bypass':
                    edge = q.domain_edge
                    def bypass(*args, **kwargs):
                        if q.mode() == 'domain_api' and kwargs.get('authenticated') is False:
                            return 200, {}, b'private bytes'
                        return edge(*args, **kwargs)
                    q.domain_edge = bypass
                self.assertFalse(q.run())
                self.assertNotIn('custom_domain_provider_outage', q.report['checks'])
                self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_corrupt_cold_serving_and_warm_cache_reuse_cannot_qualify(self):
        for mode in ('bytes', 'already_cached'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = fixture(Path(directory), recovery=False, domain_outage=False, domain=False)
                original = q.edge
                def corrupt(path='/', **kwargs):
                    status, headers, body = original(path, **kwargs)
                    if path.startswith('/_gregale/releases/') and q.mode() == 'analytics_query':
                        if mode == 'bytes':
                            body = b'incorrect bytes'
                        else:
                            headers['x-gregale-cache'] = 'HIT'
                    return status, headers, body
                q.edge = corrupt
                self.assertFalse(q.run())
                self.assertNotIn('traffic_provider_outage', q.report['checks'])
                self.assertEqual(q.report['cleanup']['status'], 'passed')

    def test_preflight_requires_explicit_flags_controller_and_new_review(self):
        for mode in ('no_controller', 'no_domain', 'no_traffic', 'existing_review', 'relative_review', 'short_observation'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
                q = fixture(Path(directory))
                if mode == 'no_controller':
                    q.args.provider_controller = None
                elif mode == 'no_domain':
                    q.args.custom_domain, q.args.domain_controller = '', None
                elif mode == 'no_traffic':
                    q.args.check_traffic = False
                elif mode == 'existing_review':
                    q.args.domain_recovery_review.write_text('{}')
                elif mode == 'relative_review':
                    q.args.domain_recovery_review = Path('review.json')
                else:
                    q.args.provider_observation_seconds = 1
                self.assertFalse(q.run())
                self.assertIsNone(q.site)
                self.assertFalse(q.journals)

    def test_controller_never_receives_runner_secrets(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = fixture(Path(directory), recovery=False, domain_outage=False, domain=False)
            q.env.update(AWS_SECRET_ACCESS_KEY='SECRET-AWS', CLOUDFLARE_API_TOKEN='SECRET-CF', SSH_AUTH_SOCK='/run/agent')
            self.assertTrue(q.run(), q.report.get('failure'))
            self.assertNotIn('SECRET', json.dumps(q.provider_calls))
            self.assertTrue(all(env == {'SSH_AUTH_SOCK': '/run/agent'} for _, _, env in q.provider_calls))

    def test_cold_probe_manifests_include_required_index_without_adding_extra_files(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = fixture(Path(directory), recovery=False, domain_outage=False, domain=False)
            captured = []
            original = q.new_release
            def record(manifest):
                captured.append(manifest)
                return original(manifest)
            q.new_release = record
            self.assertTrue(q.run(), q.report.get('failure'))
            probes = [m for m in captured if any(f['path'].startswith('probe-') for f in m['files'])]
            self.assertEqual(len(probes), 1)
            self.assertEqual(len(probes[0]['files']), 38)
            self.assertIn('index.html', {f['path'] for f in probes[0]['files']})


if __name__ == '__main__':
    unittest.main()
