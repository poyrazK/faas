#!/usr/bin/env python3
"""Portable checks for live hosting qualification; no provider credentials needed."""
from copy import deepcopy
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from urllib.error import HTTPError

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('hosting_qualification', ROOT / 'scripts/ops/internal-frontend-hosting-qualification.py')
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
SITE = '11111111-1111-4111-8111-111111111111'
ACCOUNT = '22222222-2222-4222-8222-222222222222'
ENDPOINT = 'https://' + 'a' * 32 + '.r2.cloudflarestorage.com'
BUCKET = 'gregale-internal-qualification-artifacts'


def deployment():
    return {'version': 1, 'account_id': 'a' * 32, 'hosting_domain': 'hosting.example',
            'r2_endpoint': ENDPOINT, 'bucket': BUCKET,
            'origin_id': MODULE.digest((ENDPOINT + '\n' + BUCKET).encode()),
            'worker_name': 'gregale-internal-qualification', 'worker_sha256': 'b' * 64,
            'worker_enabled': True, 'route_attached': True}


def options(root):
    return SimpleNamespace(terraform='terraform', infrastructure=root, binary='gregale', aws='aws',
                           evidence=root / 'evidence.json', project='hosting-qualification', environment='test',
                           account=ACCOUNT, edge_timeout=1, cleanup_timeout=1, check_worker_switch=False)


def switch_plan():
    before = {'account_id': 'a' * 32, 'worker_id': 'worker', 'main_module': 'worker.mjs',
              'modules': [{'name': 'worker.mjs', 'content_file': '../worker.mjs',
                           'content_type': 'application/javascript+module', 'content_sha256': 'b' * 64}],
              'annotations': {'workers_tag': 'source-hash'},
              'bindings': [{'name': 'FAAS_FRONTEND_HOSTING_ENABLED', 'type': 'plain_text', 'text': 'true'},
                           {'name': 'ORIGIN_ID', 'type': 'plain_text', 'text': 'c' * 64}]}
    after = deepcopy(before)
    after['bindings'][0]['text'] = 'false'
    after['modules'][0]['content_sha256'] = None
    return {'planned_values': {'root_module': {'resources': [{'address': 'cloudflare_worker_version.hosting', 'values': after}]}},
            'resource_changes': [{'address': 'cloudflare_worker_version.hosting',
                                 'change': {'actions': ['delete', 'create'], 'before': before, 'after': after}}]}


class FakeHTTP:
    def __init__(self):
        self.calls = []

    def request(self, method, url, headers=None, body=None, timeout=30):
        self.calls.append((method, url, headers or {}, body))
        return 200, {}, b'{}'


class LiveFixture(MODULE.Qualification):
    """Stateful provider boundary: staged bytes, immutable objects, routing and GC."""
    def __init__(self, args, *, corrupt_rollback=False, orphan=False, durable=False):
        super().__init__(args, env={'FAAS_TOKEN': 'SECRET-API', 'HOSTING_QUAL_ACCESS_TOKEN': 'SECRET-ACCESS'}, http=FakeHTTP())
        self.live_site = None
        self.releases = {}
        self.staged = {}
        self.published = {}
        self.cached = set()
        self.deleted = False
        self.corrupt_rollback = corrupt_rollback
        self.corrupt = False
        self.orphan = orphan
        self.scanned = []
        self.durable = durable

    def preflight(self):
        self.deployment = deployment()
        self.deployment['durable_routing_enabled'] = self.durable
        self.api_url = 'https://api.platform.example'
        self.checked('preflight')

    def cli(self, *args, **kwargs):
        verb = args[1]
        if verb == 'create':
            self.live_site = {'id': SITE, 'account_id': ACCOUNT, 'hostname': SITE + '.hosting.example',
                              'origin_id': self.deployment['origin_id'], 'revision': 1, 'active_release_id': '',
                              'name': args[args.index('--name') + 1], 'environment': args[args.index('--environment') + 1],
                              'durable_routing': self.durable, 'suspended': False}
        elif verb == 'deploy':
            directory = Path(args[args.index('--path') + 1])
            files = {path.relative_to(directory).as_posix(): path.read_bytes() for path in directory.rglob('*') if path.is_file()}
            release = self.new_release(MODULE.manifest(files))
            release['state'] = 'ready'
            for name, body in files.items():
                self.published[release['id'], name] = body
            self.live_site.update(active_release_id=release['id'], revision=self.live_site['revision'] + 1)
        elif verb == 'rollback':
            release = args[args.index('--release') + 1]
            self.live_site.update(active_release_id=release, revision=self.live_site['revision'] + 1)
            self.corrupt = self.corrupt_rollback
        elif verb == 'retire':
            release = args[args.index('--release') + 1]
            self.releases[release]['state'] = 'retiring'
        elif verb in ('suspend', 'resume'):
            self.live_site.update(suspended=verb == 'suspend', revision=self.live_site['revision'] + 1)
        elif verb == 'delete':
            self.deleted = True
            self.staged.clear()
            self.published.clear()
        else:
            raise AssertionError('unexpected CLI verb')
        return deepcopy(self.live_site)

    def new_release(self, manifest):
        rid = f'33333333-3333-4333-8333-{len(self.releases) + 1:012d}'
        release = {'id': rid, 'site_id': SITE, 'state': 'pending', 'manifest': deepcopy(manifest)}
        self.releases[rid] = release
        return release

    def api_response(self, method, suffix='', value=None):
        def response(status, value):
            return status, {}, json.dumps(value).encode()
        conflict = response(409, {'code': 'hosting_release_conflict'})
        if suffix == '/' + SITE:
            return response(404, {'code': 'not_found'}) if self.deleted else response(200, self.live_site)
        if suffix == '/' + SITE + '/releases':
            if method == 'POST':
                return response(201, self.new_release(value))
            return response(200, list(self.releases.values()))
        if suffix.endswith('/promotions'):
            release = self.releases[value['release_id']]
            if value['expected_revision'] != self.live_site['revision'] or release['state'] != 'ready':
                return conflict
            self.live_site.update(active_release_id=release['id'], revision=self.live_site['revision'] + 1)
            return response(202, self.live_site)
        rid = suffix.split('/')[3]
        release = self.releases[rid]
        if suffix.endswith('/uploads'):
            if release['state'] != 'pending':
                return conflict
            return response(200, {'uploads': [
                {'path': f['path'], 'url': f'{ENDPOINT}/{BUCKET}/hosting/staging/{SITE}/{rid}/{f["path"]}?signature=SECRET-GRANT'}
                for f in release['manifest']['files']]})
        if suffix.endswith('/finalize'):
            if release['state'] == 'ready':
                return response(200, release)
            for file in release['manifest']['files']:
                body = self.staged.get((rid, file['path']))
                if body is None:
                    return response(503, {'code': 'frontend_hosting_unavailable'})
                if len(body) != file['size'] or MODULE.digest(body) != file['sha256']:
                    return response(400, {'code': 'validation_failed'})
                self.published.setdefault((rid, file['path']), body)
            release['state'] = 'ready'
            return response(200, release)
        if method == 'DELETE':
            if self.live_site['active_release_id'] == rid:
                return conflict
            release['state'] = 'retiring'
            return response(202, release)
        raise AssertionError('unexpected API request')

    def upload(self, release, grant, body):
        self.staged[release, grant['path']] = body

    def edge(self, path='/', *, authenticated=True, method='GET', headers=None):
        def problem(status, code):
            return status, {}, json.dumps({'code': code}).encode()
        if not authenticated:
            return problem(302, 'login')
        if self.deleted or self.live_site.get('suspended'):
            return problem(503, 'hosting_route_unavailable')
        rid = self.live_site['active_release_id']
        name = path.lstrip('/')
        pinned = name.startswith('_gregale/releases/')
        if pinned:
            _, _, rid, name = name.split('/', 3)
            if self.releases[rid]['state'] != 'ready':
                return problem(404, 'hosting_file_not_found')
        if not name or name.endswith('/'):
            name += 'index.html'
        if (rid, name) not in self.published and not pinned:
            previous = next((other for other, key in self.published if key == name and not key.endswith('.html') and
                             self.releases[other]['state'] == 'ready'), None)
            if previous:
                rid = previous
            elif '.' not in name.rsplit('/', 1)[-1] and 'text/html' in (headers or {}).get('Accept', ''):
                name = 'index.html'
        if (rid, name) not in self.published:
            return problem(404, 'hosting_file_not_found')
        body = self.published[rid, name]
        received = {'etag': f'"sha256:{MODULE.digest(body)}"', 'x-gregale-release': rid,
                    'cache-control': 'private, max-age=0, must-revalidate', 'cf-ray': 'fixture-IAD'}
        if (headers or {}).get('If-None-Match') == received['etag']:
            return 304, received, b''
        if method == 'HEAD':
            return 200, received, b''
        received['x-gregale-cache'] = 'HIT' if (rid, name) in self.cached else 'MISS'
        self.cached.add((rid, name))
        return 200, received, b'corrupt rollback bytes' if self.corrupt else body

    def storage_count(self, prefix):
        self.scanned.append(prefix)
        if self.deleted:
            return 1 if self.orphan else 0
        return len(self.published)

    def storage_route_head(self):
        return {'version': 2, 'deleted': self.deleted, 'enabled': False, 'expires_at': 0,
                'site_id': SITE, 'hostname': self.live_site['hostname'], 'origin_id': self.live_site['origin_id'],
                'generation': self.live_site['revision'] + 1, 'active_release_id': '', 'ready_releases': []}


class HostingQualificationTests(unittest.TestCase):
    def test_durable_lifecycle_checks_suspension_and_resume(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            fixture = LiveFixture(options(Path(directory)), durable=True)
            fixture.http.request = lambda *args, **kwargs: (403, {}, b'private')
            self.assertTrue(fixture.run())
            self.assertEqual(fixture.report['routing_mode'], 'durable')
            self.assertIn('durable_site_suspended', fixture.report['checks'])
            self.assertIn('durable_site_resumed', fixture.report['checks'])
            self.assertIn('durable_deletion_head_retained', fixture.report['checks'])

    def test_durable_cleanup_requires_retained_terminal_head(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            fixture = LiveFixture(options(Path(directory)), durable=True)
            fixture.http.request = lambda *args, **kwargs: (403, {}, b'private')
            with patch.object(fixture, 'storage_route_head', return_value={}):
                self.assertFalse(fixture.run())
            self.assertEqual(fixture.report['cleanup']['status'], 'failed')
            self.assertNotIn('durable_deletion_head_retained', fixture.report['checks'])

    def test_lifecycle_requires_bytes_cache_rollback_retirement_and_cleanup(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            fixture = LiveFixture(options(Path(directory)))
            # Only the origin privacy probe uses the generic HTTPS boundary.
            fixture.http.request = lambda *args, **kwargs: (403, {}, b'private')
            self.assertTrue(fixture.run())
            report = json.loads(fixture.args.evidence.read_text())
            self.assertEqual(report['cleanup']['status'], 'passed')
            self.assertIn('edge_cache_hit', report['checks'])
            self.assertIn('immutable_publication_and_retry', report['checks'])
            self.assertIn('stale_promotion_rejected', report['checks'])
            self.assertIn('worker_deployment_switch', report['not_run'])
            self.assertEqual(fixture.scanned[-2:], [f'hosting/staging/{SITE}/', f'hosting/published/{SITE}/'])
            self.assertNotIn('SECRET', fixture.args.evidence.read_text())
            self.assertEqual(fixture.args.evidence.stat().st_mode & 0o777, 0o600)

    def test_wrong_rollback_bytes_fail_and_still_clean_up(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            fixture = LiveFixture(options(Path(directory)), corrupt_rollback=True)
            fixture.http.request = lambda *args, **kwargs: (403, {}, b'private')
            self.assertFalse(fixture.run())
            self.assertIn('incorrect bytes', fixture.report['failure'])
            self.assertEqual(fixture.report['cleanup']['status'], 'passed')
            self.assertNotIn('rollback', fixture.report['checks'])

    def test_missing_metadata_does_not_prove_storage_cleanup(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            fixture = LiveFixture(options(Path(directory)), orphan=True)
            fixture.http.request = lambda *args, **kwargs: (403, {}, b'private')
            self.assertFalse(fixture.run())
            self.assertEqual(fixture.report['cleanup']['status'], 'failed')
            self.assertNotIn('artifact_and_metadata_cleanup', fixture.report['checks'])

    def test_existing_evidence_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            args = options(Path(directory))
            args.evidence.write_text('previous observations')
            with self.assertRaises(FileExistsError):
                LiveFixture(args).run()
            self.assertEqual(args.evidence.read_text(), 'previous observations')

    def test_credentials_remain_scoped_to_api_edge_and_staging(self):
        with tempfile.TemporaryDirectory() as directory:
            http = FakeHTTP()
            q = MODULE.Qualification(options(Path(directory)), env={'FAAS_TOKEN': 'api-secret', 'HOSTING_QUAL_ACCESS_TOKEN': 'access-secret'}, http=http)
            q.api_url = 'https://api.platform.example'
            q.site = {'id': SITE, 'hostname': SITE + '.hosting.example'}
            q.deployment = deployment()
            q.api_response('POST', '/' + SITE + '/releases', {})
            q.edge()
            q.edge(authenticated=False)
            rid = '33333333-3333-4333-8333-333333333333'
            grant = {'path': 'index.html', 'url': f'{ENDPOINT}/{BUCKET}/hosting/staging/{SITE}/{rid}/index.html?signature=secret', 'headers': {}}
            q.upload(rid, grant, b'html')
            self.assertEqual(http.calls[0][2]['Authorization'], 'Bearer api-secret')
            self.assertIn('Idempotency-Key', http.calls[0][2])
            self.assertNotIn('Cookie', http.calls[0][2])
            self.assertEqual(http.calls[1][2], {'Cookie': 'CF_Authorization=access-secret'})
            self.assertEqual(http.calls[2][2], {})
            self.assertEqual(http.calls[3][2], {'Content-Type': 'text/html'})
            for invalid in [dict(grant, url=grant['url'].replace('staging', 'published')),
                            dict(grant, url=grant['url'].replace(ENDPOINT, 'https://other.example')),
                            dict(grant, headers={'Authorization': ['Bearer secret']})]:
                with self.assertRaises(MODULE.Failure):
                    q.upload(rid, invalid, b'html')
            self.assertEqual(len(http.calls), 4)
            # Later API responses cannot redirect the Access cookie elsewhere.
            q.site['hostname'] = 'attacker.example'
            q.edge()
            self.assertEqual(http.calls[-1][1], 'https://' + SITE + '.hosting.example/')

    def test_redirect_is_returned_without_following_it(self):
        http = MODULE.HTTP()
        error = HTTPError('https://private.example/', 302, 'redirect', {'Location': 'https://attacker.example/'}, io.BytesIO(b'login'))
        with patch.object(http.opener, 'open', side_effect=error) as opened:
            self.assertEqual(http.request('GET', 'https://private.example/'),
                             (302, {'location': 'https://attacker.example/'}, b'login'))
            self.assertEqual(opened.call_count, 1)
        self.assertIsNone(MODULE.NoRedirect().redirect_request(None, None, 302, 'redirect', {}, 'https://attacker.example/'))

    def test_subprocess_failures_do_not_expose_output_or_secret_arguments(self):
        with patch.object(MODULE.subprocess, 'run', return_value=SimpleNamespace(returncode=1, stdout=b'SECRET', stderr=b'SECRET')):
            with self.assertRaisesRegex(MODULE.Failure, '^Qualification command failed;') as error:
                MODULE.execute(['tool', 'SECRET'], {})
            self.assertNotIn('SECRET', str(error.exception))
        with patch.object(MODULE.subprocess, 'run', side_effect=subprocess.TimeoutExpired(['SECRET'], 1)):
            with self.assertRaisesRegex(MODULE.Failure, '^Qualification command unavailable or timed out$'):
                MODULE.execute(['SECRET'], {})

    def test_live_privacy_check_inspects_both_r2_domains_and_worker_alternate_urls(self):
        with tempfile.TemporaryDirectory() as directory, patch('builtins.print'):
            q = MODULE.Qualification(options(Path(directory)), env={})
            q.deployment = deployment()
            private = [{'enabled': False}, {'domains': []}, {'enabled': False, 'previews_enabled': False}]
            with patch.object(q, 'cloudflare', side_effect=private) as inspected:
                q.check_private_endpoints()
            self.assertEqual(inspected.call_count, 3)
            for public in ([{'enabled': True}, private[1], private[2]],
                           [private[0], {'domains': [{'domain': 'public.example'}]}, private[2]],
                           [private[0], private[1], {'enabled': False, 'previews_enabled': True}]):
                with patch.object(q, 'cloudflare', side_effect=public), self.assertRaises(MODULE.Failure):
                    q.check_private_endpoints()

    def test_default_off_preflight_performs_no_external_actions(self):
        with tempfile.TemporaryDirectory() as directory:
            q = MODULE.Qualification(options(Path(directory)), env={}, http=FakeHTTP())
            with patch.object(q.infrastructure, 'output') as output, patch.object(q, 'cli') as cli:
                with self.assertRaisesRegex(MODULE.Failure, 'internal CLI flag'):
                    q.preflight()
                output.assert_not_called()
                cli.assert_not_called()
            self.assertEqual(q.http.calls, [])

    def test_deployment_requires_enabled_isolated_provider_identity(self):
        self.assertEqual(MODULE.validate_deployment(deployment())['bucket'], BUCKET)
        for change in ({'worker_enabled': False}, {'route_attached': False}, {'r2_endpoint': 'https://other.example'},
                       {'origin_id': 'c' * 64}, {'bucket': 'customers'}, {'hosting_domain': 'hosting.example/other'}):
            with self.subTest(change=change), self.assertRaises(MODULE.Failure):
                MODULE.validate_deployment({**deployment(), **change})

    def test_switch_plan_refuses_unrelated_drift_source_and_binding_changes(self):
        MODULE.validate_switch_plan(switch_plan(), True, False)
        plan = switch_plan()
        plan['resource_changes'].append({'address': 'cloudflare_dns_record.hosting[0]', 'change': {'actions': ['delete']}})
        with self.assertRaisesRegex(MODULE.Failure, 'unrelated infrastructure'):
            MODULE.validate_switch_plan(plan, True, False)
        for field, value in [('annotations', {'workers_tag': 'new-source'}), ('limits', {'cpu_ms': 100})]:
            plan = switch_plan()
            plan['resource_changes'][0]['change']['after'][field] = value
            with self.assertRaisesRegex(MODULE.Failure, 'source or configuration'):
                MODULE.validate_switch_plan(plan, True, False)
        plan = switch_plan()
        plan['resource_changes'][0]['change']['after']['bindings'][1]['text'] = 'new-origin'
        with self.assertRaisesRegex(MODULE.Failure, 'bindings'):
            MODULE.validate_switch_plan(plan, True, False)

    def test_restore_allows_noop_after_an_interrupted_disable(self):
        noop = switch_plan()
        noop['resource_changes'] = []
        noop['planned_values']['root_module']['resources'][0]['values']['bindings'][0]['text'] = 'true'
        MODULE.validate_switch_plan(noop, False, True, restoring=True)
        with self.assertRaises(MODULE.Failure):
            MODULE.validate_switch_plan({'resource_changes': []}, True, False)
        with self.assertRaises(MODULE.Failure):
            MODULE.validate_switch_plan(switch_plan(), False, True, restoring=True)
        with tempfile.TemporaryDirectory() as directory:
            q = MODULE.Qualification(options(Path(directory)), env={})
            infra = SimpleNamespace(switch=lambda *args, **kwargs: None)
            q.infrastructure = infra
            calls = []
            def interrupted(*args, **kwargs):
                calls.append((args, kwargs))
                if len(calls) == 1:
                    raise MODULE.Failure('Apply interrupted')
            infra.switch = interrupted
            with self.assertRaisesRegex(MODULE.Failure, 'Apply interrupted'):
                q.check_switch('release', b'html')
            self.assertEqual(calls, [((True, False), {}), ((False, True), {'restoring': True})])


if __name__ == '__main__':
    unittest.main()
