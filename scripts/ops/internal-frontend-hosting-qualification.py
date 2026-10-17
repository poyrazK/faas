#!/usr/bin/env python3
"""Qualify an explicitly configured, disposable internal static-hosting pilot."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.parse import unquote, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parent))
from internal_frontend_hosting_probes import PilotProbes
from internal_frontend_hosting_acceptance import AcceptanceChecks
from internal_frontend_hosting_provider_faults import ProviderFaultChecks

ROOT = Path(__file__).resolve().parents[2]
API_PATH = '/v1/internal/hosting/sites'
UUID = re.compile(r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
DIGEST = re.compile(r'^[0-9a-f]{64}$')


class Failure(Exception):
    """Messages are fixed diagnostics, never upstream response bodies or URLs."""


def require(condition, message):
    if not condition:
        raise Failure(message)


def utc_now():
    return datetime.now(timezone.utc).isoformat()


def digest(value):
    return hashlib.sha256(value).hexdigest()


def execute(command, env, timeout=120, accepted=(0,), input=None):
    try:
        result = subprocess.run(command, env=env, capture_output=True, timeout=timeout, input=input)
    except (OSError, subprocess.TimeoutExpired):
        raise Failure('Qualification command unavailable or timed out') from None
    require(result.returncode in accepted, 'Qualification command failed; inspect the configured service separately')
    return result


def decode_json(body):
    try:
        return json.loads(body)
    except (ValueError, TypeError, UnicodeError):
        raise Failure('Expected a JSON response') from None


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class HTTP:
    def __init__(self):
        self.opener = build_opener(NoRedirect())

    def request(self, method, url, headers=None, body=None, timeout=30):
        try:
            response = self.opener.open(Request(url, data=body, method=method, headers=headers or {}), timeout=timeout)
        except HTTPError as error:
            response = error
        except (OSError, URLError, ValueError):
            raise Failure('HTTPS request failed; verify connectivity and credentials separately') from None
        with response:
            payload = response.read(1024 * 1024 + 1)
            require(len(payload) <= 1024 * 1024, 'Qualification response exceeded its fixture bound')
            return response.code, {k.lower(): v for k, v in response.headers.items()}, payload


def validate_deployment(value):
    require(isinstance(value, dict) and value.get('version') == 1, 'Unknown infrastructure output format')
    require(value.get('worker_enabled') is True and value.get('route_attached') is True,
            'The dedicated pilot must already have its route and Worker explicitly enabled')
    account = value.get('account_id', '')
    domain = value.get('hosting_domain', '')
    bucket = value.get('bucket', '')
    require(isinstance(account, str) and re.fullmatch(r'[0-9a-f]{32}', account), 'Invalid infrastructure account')
    require(isinstance(domain, str) and re.fullmatch(r'([a-z0-9][a-z0-9-]*\.)+[a-z]{2,}', domain), 'Invalid hosting domain')
    require(isinstance(bucket, str) and re.fullmatch(r'gregale-internal-[a-z0-9-]+', bucket), 'Expected a dedicated internal bucket')
    require(value.get('r2_endpoint') == f'https://{account}.r2.cloudflarestorage.com', 'Unexpected storage endpoint')
    require(value.get('origin_id') == digest((value['r2_endpoint'] + '\n' + bucket).encode()), 'Infrastructure origin fingerprint mismatch')
    require(isinstance(value.get('worker_sha256'), str) and DIGEST.fullmatch(value['worker_sha256']), 'Missing Worker source digest')
    require(isinstance(value.get('worker_name'), str) and value['worker_name'].startswith('gregale-internal-'), 'Expected a dedicated internal Worker')
    return value


def validate_switch_plan(plan, before_enabled, after_enabled, restoring=False):
    """An opt-in switch check may mutate only this module's version/deployment."""
    resources = plan.get('planned_values', {}).get('root_module', {}).get('resources', [])
    version = next((r.get('values', {}) for r in resources if r.get('address') == 'cloudflare_worker_version.hosting'), {})
    flags = [b.get('text') for b in version.get('bindings', []) if b.get('name') == 'FAAS_FRONTEND_HOSTING_ENABLED']
    require(flags == [str(after_enabled).lower()], 'Planned Worker does not contain the requested feature switch')
    allowed = {'cloudflare_worker_version.hosting', 'cloudflare_workers_deployment.hosting'}
    changed = set()
    for change in plan.get('resource_changes', []):
        actions = change['change']['actions']
        if actions in (['no-op'], ['read']):
            continue
        address = change['address']
        require(address in allowed, 'Switch plan includes unrelated infrastructure changes; reconcile them separately')
        changed.add(address)
        if address == 'cloudflare_worker_version.hosting':
            before, after = change['change']['before'], change['change']['after']
            require(isinstance(before, dict) and isinstance(after, dict), 'Switch plan must update an existing Worker version')
            unknown = change['change'].get('after_unknown', {})
            for field in ('account_id', 'worker_id', 'main_module', 'compatibility_date', 'compatibility_flags',
                          'assets', 'limits', 'migrations', 'placement', 'usage_model'):
                recomputed_default = field in ('compatibility_flags', 'limits', 'usage_model') and unknown.get(field) is True
                require(before.get(field) == after.get(field) or recomputed_default,
                        'Switch plan also changes Worker source or configuration')
            for key in ('workers_tag', 'workers_message'):
                require((before.get('annotations') or {}).get(key) == (after.get('annotations') or {}).get(key),
                        'Switch plan also changes Worker source or configuration')
            # The provider recomputes read-only module hashes on replacement.
            def modules(version):
                return sorted((m['name'], m['content_file'], m['content_type']) for m in version.get('modules', []))
            require(modules(before) == modules(after), 'Switch plan also changes Worker modules')
            old_bindings = {b['name']: b for b in before.get('bindings', [])}
            new_bindings = {b['name']: b for b in after.get('bindings', [])}
            flag = 'FAAS_FRONTEND_HOSTING_ENABLED'
            require(old_bindings.get(flag, {}).get('text') in (('true', 'false') if restoring else (str(before_enabled).lower(),)) and
                    new_bindings.get(flag, {}).get('text') == str(after_enabled).lower(), 'Switch plan has unexpected flag values')
            old_bindings.pop(flag, None)
            new_bindings.pop(flag, None)
            require(old_bindings == new_bindings, 'Switch plan also changes Worker bindings')
        else:
            before, after = change['change']['before'], change['change']['after']
            require(isinstance(before, dict) and isinstance(after, dict), 'Switch must update an existing deployment')
            require(all(before.get(k) == after.get(k) for k in ('account_id', 'script_name', 'strategy')),
                    'Switch plan changes deployment configuration')
            unknown = change['change'].get('after_unknown', {})
            require(unknown.get('annotations') is True or
                    (before.get('annotations') or {}).get('workers_message') == (after.get('annotations') or {}).get('workers_message'),
                    'Switch plan changes deployment annotations')
            require(len(after.get('versions', [])) == 1 and after['versions'][0].get('percentage') == 100,
                    'Switch plan changes the deployment traffic split')
    require(restoring or 'cloudflare_worker_version.hosting' in changed, 'Switch plan does not change the Worker flag')


class Infrastructure:
    def __init__(self, binary, directory, env):
        self.binary = binary
        self.directory = directory.resolve()
        # Ambient Terraform flags or logging must not alter the reviewed commands.
        self.env = {k: v for k, v in env.items() if not k.startswith('TF_CLI_ARGS') and k not in ('TF_LOG', 'TF_LOG_PATH')}
        self.env.update(TF_IN_AUTOMATION='true', TF_INPUT='false')

    def run(self, *args, **kwargs):
        return execute([self.binary, f'-chdir={self.directory}', *args], self.env, **kwargs)

    def output(self):
        return validate_deployment(decode_json(self.run('output', '-json', 'hosting').stdout))

    def check_clean(self):
        result = self.run('plan', '-input=false', '-no-color', '-detailed-exitcode', accepted=(0, 2), timeout=300)
        require(result.returncode == 0, 'Infrastructure has pending changes; apply and review them before the switch check')

    def switch(self, before_enabled, after_enabled, restoring=False):
        with tempfile.TemporaryDirectory(prefix='gregale-hosting-switch-') as directory:
            plan = str(Path(directory) / 'switch.tfplan')
            self.run('plan', '-input=false', '-no-color', f'-var=worker_enabled={str(after_enabled).lower()}',
                     f'-out={plan}', timeout=300)
            value = decode_json(self.run('show', '-json', plan).stdout)
            validate_switch_plan(value, before_enabled, after_enabled, restoring)
            self.run('apply', '-input=false', '-no-color', plan, timeout=300)


def fixtures(run_id, label):
    script = f'console.log("hosting qualification {run_id} {label}");\n'.encode()
    path = f'assets/{digest(script)}.js'
    html = f'<!doctype html><title>{run_id} {label}</title><script src="/{path}"></script>\n'.encode()
    return {'index.html': html, path: script, 'help/index.html': f'{run_id} {label} help\n'.encode()}


def manifest(files):
    return {'spa_fallback': True, 'files': [
        {'path': path, 'sha256': digest(body), 'size': len(body),
         'content_type': 'text/html' if path.endswith('.html') else 'text/javascript'}
        for path, body in files.items()
    ]}


class Qualification(PilotProbes, AcceptanceChecks, ProviderFaultChecks):
    def __init__(self, args, env=None, http=None):
        self.args = args
        self.env = dict(os.environ if env is None else env)
        self.http = http or HTTP()
        self.infrastructure = Infrastructure(args.terraform, args.infrastructure, self.env)
        self.site = None
        self.deployment = None
        for name, default in {'outage_controller': None, 'check_control_plane_outages': False,
                              'check_publication_recovery': False, 'outage_duration': 1200,
                              'outage_interval': 30, 'edge_samples': 0, 'runner_region': '', 'resource_profile': False,
                              'check_usage': False, 'check_traffic': False, 'custom_domain': '',
                              'domain_controller': None, 'provider_controller': None,
                              'check_domain_recovery': False, 'domain_recovery_review': None,
                              'check_domain_provider_outage': False, 'check_analytics_outage': False,
                              'provider_timeout': 2400, 'provider_observation_seconds': 180}.items():
            if not hasattr(args, name):
                setattr(args, name, default)
        self.configure_probes(require, decode_json, execute, Failure)
        self.configure_acceptance()
        self.configure_provider_faults()
        self.probe_digest, self.probe_uuid = digest, UUID
        self.report = {'version': 2, 'scope': 'internal-static-frontend-hosting', 'status': 'running',
                       'started_at': utc_now(), 'checks': {}, 'public_launch_qualified': False,
                       'not_run': ['api_control_plane_outage', 'database_control_plane_outage',
                                   'regional_latency_and_load', 'publication_retry_recovery',
                                   'provider_usage_billing', 'control_plane_resource_profile',
                                   'account_quota_admission', 'account_reservation_cleanup', 'traffic_observations',
                                   'custom_domain_lifecycle', 'custom_domain_ambiguous_create_recovery',
                                   'custom_domain_provider_outage', 'certificate_renewal', 'traffic_provider_outage']}
        if not args.check_worker_switch:
            self.report['not_run'].append('worker_deployment_switch')

    def save(self):
        with tempfile.NamedTemporaryFile(mode='w', dir=self.args.evidence.parent, delete=False) as stream:
            path = Path(stream.name)
            json.dump(self.report, stream, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        try:
            os.replace(path, self.args.evidence)
        finally:
            path.unlink(missing_ok=True)

    def checked(self, name, **evidence):
        self.report['checks'][name] = {'status': 'passed', 'at': utc_now(), **evidence}
        self.save()
        print(f'hosting qualification: {name} passed', flush=True)

    def cli(self, *args, timeout=180):
        return decode_json(execute([self.args.binary, '--json', *args], self.env, timeout=timeout).stdout)

    def accept_site(self, value, expected_id):
        require(isinstance(value, dict) and value.get('id') == expected_id and
                value.get('hostname') == expected_id + '.' + self.deployment['hosting_domain'] and
                value.get('account_id') == self.args.account and value.get('origin_id') == self.deployment['origin_id'] and
                type(value.get('revision')) is int and value['revision'] >= 1 and
                type(value.get('durable_routing')) is bool and type(value.get('suspended')) is bool and
                (self.site is None or value['durable_routing'] == self.site['durable_routing']),
                'Site response changed account, hostname, identity, revision, routing mode, or storage placement')
        return value

    def api_response(self, method, suffix='', value=None, *, timeout=180, root=API_PATH):
        body = None if value is None else json.dumps(value).encode()
        headers = {'Authorization': 'Bearer ' + self.env['FAAS_TOKEN'], 'Content-Type': 'application/json'}
        if method not in ('GET', 'HEAD'):
            headers['Idempotency-Key'] = str(uuid.uuid4())
        return self.http.request(method, self.api_url + root + suffix,
                                 headers, body, timeout=timeout)

    def api(self, method, suffix='', value=None, status=200, code=None):
        actual, _, body = self.api_response(method, suffix, value)
        require(actual == status, f'Hosting API returned unexpected HTTP status for {method}')
        data = decode_json(body)
        if code:
            require(data.get('code') == code, 'Hosting API returned an unexpected problem code')
        return data

    def cloudflare(self, suffix):
        url = 'https://api.cloudflare.com/client/v4/accounts/' + self.deployment['account_id'] + suffix
        status, _, body = self.http.request('GET', url, {'Authorization': 'Bearer ' + self.env['CLOUDFLARE_API_TOKEN']})
        require(status == 200, 'Cloudflare infrastructure inspection failed')
        value = decode_json(body)
        require(value.get('success') is True and 'result' in value, 'Cloudflare infrastructure inspection was not acknowledged')
        return value['result']

    def check_private_endpoints(self):
        bucket_path = '/r2/buckets/' + self.deployment['bucket'] + '/domains/'
        managed = self.cloudflare(bucket_path + 'managed')
        custom = self.cloudflare(bucket_path + 'custom')
        worker = self.cloudflare('/workers/scripts/' + self.deployment['worker_name'] + '/subdomain')
        require(managed.get('enabled') is False and custom.get('domains') == [],
                'R2 public access or a custom bucket domain is configured')
        require(worker.get('enabled') is False and worker.get('previews_enabled') is False,
                'Alternate workers.dev or preview serving is enabled')
        self.checked('alternate_endpoints_disabled')

    def access_token(self):
        path = self.env.get('HOSTING_QUAL_ACCESS_TOKEN_FILE')
        if path:
            from internal_frontend_hosting_pilot_common import PilotError, private_read
            try:
                token = private_read(Path(path), 16384).decode().strip()
            except (PilotError, OSError, ValueError):
                raise Failure('Cannot read the protected canonical Access token file') from None
        else:
            token = self.env.get('HOSTING_QUAL_ACCESS_TOKEN', '')
        require(re.fullmatch(r'[A-Za-z0-9_.-]{1,16384}', token) is not None, 'Invalid canonical Access token encoding')
        return token

    def edge(self, path='/', *, authenticated=True, method='GET', headers=None):
        values = dict(headers or {})
        if authenticated:
            values['Cookie'] = 'CF_Authorization=' + self.access_token()
        sid = self.site['id']
        require(isinstance(sid, str) and UUID.fullmatch(sid), 'Invalid site identity for edge request')
        # Derive the destination instead of forwarding a cookie to an API-
        # supplied hostname that might change in a later response.
        return self.http.request(method, 'https://' + sid + '.' + self.deployment['hosting_domain'] + path, values)

    def wait(self, name, check, timeout=None):
        started = time.monotonic()
        while True:
            result = check()
            if result:
                return result, round(time.monotonic() - started, 3)
            require(time.monotonic() - started < (timeout or self.args.edge_timeout), f'Timed out waiting for {name}')
            time.sleep(2)

    def expect_file(self, path, release, body, *, cache_hit=False, headers=None):
        def check():
            status, received, data = self.edge(path, headers=headers)
            if status in (401, 403) or 300 <= status < 400:
                raise Failure('Authenticated edge request was denied; refresh the Access identity token')
            if status != 200 or received.get('x-gregale-release') != release:
                return False
            require(data == body, 'Edge served incorrect bytes for the expected release')
            require(received.get('etag') == f'"sha256:{digest(body)}"', 'Edge ETag did not match the fixture digest')
            require(received.get('cache-control') == 'private, max-age=0, must-revalidate', 'Browser cache policy changed')
            if cache_hit and received.get('x-gregale-cache') != 'HIT':
                return False
            return {'cache': received.get('x-gregale-cache'), 'colo': received.get('cf-ray', '').rsplit('-', 1)[-1]}
        result, elapsed = self.wait('expected edge release', check)
        return {**result, 'observed_after_seconds': elapsed, 'release_id': release}

    def denied(self, path='/', method='GET', headers=None):
        status, received, _ = self.edge(path, authenticated=False, method=method, headers=headers)
        require(status in (302, 303, 401, 403) and 'x-gregale-release' not in received and
                'x-gregale-cache' not in received,
                'An unauthenticated request was not denied by Access')
        return status

    def upload(self, release, grant, body):
        url = urlsplit(grant['url'])
        endpoint = urlsplit(self.deployment['r2_endpoint'])
        expected = f"/{self.deployment['bucket']}/hosting/staging/{self.site['id']}/{release}/{grant['path']}"
        require(url.scheme == 'https' and url.netloc == endpoint.netloc and unquote(url.path) == expected and
                not url.username and not url.fragment, 'Upload grant escaped the dedicated staging namespace')
        headers = {}
        for name, values in grant.get('headers', {}).items():
            require(name.lower() == 'content-type' or name.lower().startswith('x-amz-'), 'Upload grant contains a credential-forwarding header')
            require(isinstance(values, list) and len(values) == 1 and '\r' not in values[0] and '\n' not in values[0], 'Invalid upload grant headers')
            headers[name] = values[0]
        headers['Content-Type'] = 'text/html' if grant['path'].endswith('.html') else 'text/javascript'
        status, _, _ = self.http.request('PUT', grant['url'], headers, body)
        require(200 <= status < 300, 'Private staging upload failed')

    def storage(self, *args):
        env = dict(self.env, AWS_ACCESS_KEY_ID=self.env['HOSTING_QUAL_R2_ACCESS_KEY'],
                   AWS_SECRET_ACCESS_KEY=self.env['HOSTING_QUAL_R2_SECRET_KEY'], AWS_DEFAULT_REGION='auto',
                   AWS_EC2_METADATA_DISABLED='true', AWS_CONFIG_FILE=os.devnull, AWS_SHARED_CREDENTIALS_FILE=os.devnull,
                   AWS_PAGER='')
        env.pop('AWS_SESSION_TOKEN', None)
        return decode_json(execute([self.args.aws, '--endpoint-url', self.deployment['r2_endpoint'], '--no-cli-pager',
                                    '--no-paginate', 's3api', *args, '--output', 'json'], env, timeout=45).stdout)

    def storage_count(self, prefix):
        value = self.storage('list-objects-v2', '--bucket', self.deployment['bucket'], '--prefix', prefix)
        require(type(value.get('KeyCount')) is int and value['KeyCount'] >= 0 and value.get('IsTruncated') is False,
                'Storage listing did not return a complete bounded prefix')
        return value['KeyCount']

    def storage_route_head(self):
        with tempfile.TemporaryDirectory(prefix='gregale-hosting-route-') as directory:
            path = Path(directory) / 'head.json'
            self.storage('get-object', '--bucket', self.deployment['bucket'],
                         '--key', 'hosting/routes/' + self.site['hostname'] + '.json',
                         '--range', 'bytes=0-8192', str(path))
            body = path.read_bytes()
            require(len(body) <= 8192, 'Retained routing head exceeded its size bound')
            return decode_json(body)

    def preflight(self):
        require(self.env.get('FAAS_FRONTEND_HOSTING_ENABLED', '').lower() in ('1', 'true'), 'Enable the internal CLI flag explicitly for qualification')
        for key in ('FAAS_API', 'FAAS_TOKEN', 'CLOUDFLARE_API_TOKEN',
                    'HOSTING_QUAL_R2_ACCESS_KEY', 'HOSTING_QUAL_R2_SECRET_KEY'):
            require(bool(self.env.get(key)), f'Missing required environment binding: {key}')
        url = urlsplit(self.env['FAAS_API'])
        require(url.scheme == 'https' and url.hostname and url.username is None and url.password is None and not url.query and not url.fragment and
                url.path in ('', '/'), 'FAAS_API must name an explicit HTTPS API origin')
        self.api_url = self.env['FAAS_API'].rstrip('/')
        self.access_token()
        self.deployment = self.infrastructure.output()
        domain = self.deployment['hosting_domain']
        require(url.hostname != domain and not url.hostname.endswith('.' + domain) and not domain.endswith('.' + url.hostname),
                'Hosting and platform API origins must be isolated')
        worker = ROOT / 'deploy/cloudflare/hosting-worker/worker.mjs'
        require(digest(worker.read_bytes()) == self.deployment['worker_sha256'], 'Deployed infrastructure output does not match this checkout Worker')
        require(UUID.fullmatch(self.args.account) is not None, 'An expected disposable qualification account UUID is required')
        account = self.cli('whoami')
        require(account.get('id') == self.args.account, 'Authenticated account differs from the expected qualification account')
        self.prepare_probes()
        if self.args.check_worker_switch:
            self.infrastructure.check_clean()
        self.report['deployment'] = {k: self.deployment[k] for k in ('account_id', 'hosting_domain', 'bucket', 'origin_id', 'worker_name', 'worker_sha256')}
        self.report['account_id'] = self.args.account
        binary = Path(shutil.which(self.args.binary) or self.args.binary)
        self.report['cli_sha256'] = digest(binary.read_bytes())
        self.check_private_endpoints()
        self.prepare_acceptance()
        self.prepare_provider_faults()
        self.checked('preflight')

    def interrupted_release(self, files):
        suffix = '/' + self.site['id']
        release = self.api('POST', suffix + '/releases', manifest(files), status=201)
        rid = release['id']
        require(isinstance(rid, str) and UUID.fullmatch(rid), 'Invalid pending release identity')
        self.report['interrupted_release_id'] = rid
        self.save()
        base = suffix + '/releases/' + rid
        grants = self.api('POST', base + '/uploads')['uploads']
        require({g['path'] for g in grants} == set(files) and len(grants) == len(files), 'Upload grants did not match the fixture')
        # Publish one valid file and then stop uploading; finalization must fail
        # and a pending release must never become active.
        self.upload(rid, grants[0], files[grants[0]['path']])
        status, _, _ = self.api_response('POST', base + '/finalize')
        require(status in (400, 503), 'Incomplete upload was incorrectly finalized')
        self.api('POST', suffix + '/promotions', {'release_id': rid, 'expected_revision': self.site['revision']},
                 status=409, code='hosting_release_conflict')
        rows = self.api('GET', suffix + '/releases')
        require(any(row['id'] == rid and row['state'] == 'pending' for row in rows), 'Interrupted release did not remain pending')
        self.checked('incomplete_upload_rejected')
        for grant in grants:
            original = files[grant['path']]
            self.upload(rid, grant, b'x' * len(original) if grant['path'] == 'index.html' else original)
        self.api('POST', base + '/finalize', status=400, code='validation_failed')
        self.checked('digest_mismatch_rejected')
        grants = self.api('POST', base + '/uploads')['uploads']
        for grant in grants:
            self.upload(rid, grant, files[grant['path']])
        require(self.api('POST', base + '/finalize')['state'] == 'ready', 'Recovered release was not ready')
        require(self.api('POST', base + '/finalize')['state'] == 'ready', 'Finalization retry changed the release')
        self.api('POST', base + '/uploads', status=409, code='hosting_release_conflict')
        # A still-valid old grant can change only staging, never published bytes.
        grant = next(g for g in grants if g['path'] == 'index.html')
        self.upload(rid, grant, b'x' * len(files['index.html']))
        self.site = self.accept_site(self.api('POST', suffix + '/promotions',
                                             {'release_id': rid, 'expected_revision': self.site['revision']}, status=202), self.site['id'])
        self.checked('immutable_publication_and_retry', **self.expect_file('/', rid, files['index.html']))
        self.api('DELETE', base, status=409, code='hosting_release_conflict')
        return rid

    def deploy(self, files, directory):
        directory.mkdir()
        for name, body in files.items():
            path = directory / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(body)
        previous = self.site
        current = self.cli('sites', 'deploy', '--site', previous['id'], '--path', str(directory), '--spa')
        self.accept_site(current, previous['id'])
        rid = current.get('active_release_id', '')
        require(current.get('id') == previous['id'] and UUID.fullmatch(rid) and
                rid != previous.get('active_release_id') and current.get('revision', 0) > previous['revision'],
                'CLI deployment did not advance this site to a new release')
        self.site = current
        self.report.setdefault('release_ids', []).append(rid)
        self.save()
        return rid

    def check_switch(self, release, body):
        try:
            self.infrastructure.switch(True, False)
            def disabled():
                status, headers, payload = self.edge()
                return status == 404 and 'x-gregale-release' not in headers and decode_json(payload).get('code') == 'not_found'
            _, elapsed = self.wait('Worker deployment switch', disabled)
            self.checked('worker_deployment_switch_disabled', observed_after_seconds=elapsed)
        finally:
            # Always reconcile the original flag, even if an interrupted apply
            # changed serving before it could update Terraform's output values.
            try:
                self.infrastructure.switch(False, True, restoring=True)
            except (Failure, OSError, ValueError, KeyError, TypeError):
                self.report['worker_switch_restore'] = {'status': 'failed', 'action': 'Review/apply worker_enabled=true for the recorded dedicated Worker'}
                self.save()
                raise Failure('Worker switch restoration failed; restore the recorded pilot before further qualification') from None
        self.checked('worker_deployment_switch_restored', **self.expect_file('/', release, body))

    def exercise(self):
        run_id = uuid.uuid4().hex
        self.report.update(site_name='hosting-qual-' + run_id[:12], project=self.args.project, environment=self.args.environment)
        self.save()
        created = self.cli('sites', 'create', '--name', self.report['site_name'], '--project', self.args.project,
                           '--environment', self.args.environment)
        sid = created.get('id', '')
        require(isinstance(sid, str) and UUID.fullmatch(sid), 'CLI did not return a site UUID')
        self.report['site_id'] = sid
        self.save()
        require(created.get('name') == self.report['site_name'] and created.get('environment') == self.args.environment,
                'Created site did not match the disposable qualification name and environment')
        self.site = self.accept_site(created, sid)
        require(not (self.args.check_control_plane_outages or self.args.check_publication_recovery or
                     self.args.edge_samples or self.args.custom_domain) or created['durable_routing'],
                'Outage, regional and custom-domain probes require a newly created durable site')
        self.report['routing_mode'] = 'durable' if created.get('durable_routing') is True else 'leased'
        require(not created.get('durable_routing') or self.deployment.get('durable_routing_enabled') is True,
                'The API created a durable site but the Worker durable-routing switch is disabled')
        if not created.get('durable_routing'):
            self.report['not_run'].append('durable_site_suspension')
        self.checked('access_denied_before_publication', http_status=self.denied())
        if self.args.check_usage:
            self.quota_admission()
        if self.args.custom_domain:
            self.create_domain()
        self.interrupted_release(fixtures(run_id, 'recovered'))
        self.check_usage_snapshot('after_finalization')
        with tempfile.TemporaryDirectory(prefix='gregale-hosting-qualification-') as temp:
            old, new = fixtures(run_id, 'first'), fixtures(run_id, 'second')
            first = self.deploy(old, Path(temp) / 'first')
            first_revision = self.site['revision']
            self.checked('first_deployment', **self.expect_file('/', first, old['index.html']))
            self.access_contract(first, old)
            if self.args.custom_domain:
                if self.args.check_domain_recovery:
                    self.ambiguous_domain_recovery(first, old)
                else:
                    self.ready_domain()
                self.domain_contract('first_deployment', first, old)
            self.checked('edge_cache_hit', **self.expect_file('/', first, old['index.html'], cache_hit=True))
            self.checked('access_denied_after_cache_hit', http_status=self.denied())
            asset = next(path for path in old if path.endswith('.js'))
            status, _, _ = self.http.request('GET', self.deployment['r2_endpoint'] + '/' + self.deployment['bucket'] +
                                            f'/hosting/published/{sid}/{first}/manifest.json')
            require(status in (401, 403), 'Published bucket was reachable without storage authorization')
            require(self.storage_count(f'hosting/published/{sid}/') > 0, 'Authenticated storage inventory did not find published fixtures')
            self.checked('private_origin')
            status, _, body = self.edge('/help/', headers={'Accept': 'text/html'})
            require(status == 200 and body == old['help/index.html'], 'Directory index routing failed')
            self.expect_file('/client/navigation', first, old['index.html'], headers={'Accept': 'text/html'})
            for path, accept in (('/missing.js', 'text/html'), ('/api/missing', 'application/json')):
                status, _, body = self.edge(path, headers={'Accept': accept})
                require(status == 404 and decode_json(body).get('code') == 'hosting_file_not_found', 'SPA fallback masked a missing asset or API request')
            status, headers, body = self.edge('/', method='HEAD')
            require(status == 200 and not body and headers.get('x-gregale-release') == first, 'HEAD returned incorrect release metadata')
            status, _, body = self.edge('/', headers={'If-None-Match': f'"sha256:{digest(old["index.html"])}"'})
            require(status == 304 and not body, 'Conditional request did not revalidate')
            self.checked('static_http_contract')
            second = self.deploy(new, Path(temp) / 'second')
            self.checked('updated_deployment', **self.expect_file('/', second, new['index.html']))
            if self.args.custom_domain:
                self.domain_contract('promotion', second, new)
            new_asset = next(path for path in new if path.endswith('.js'))
            self.expect_file('/' + new_asset, second, new[new_asset])
            self.expect_file('/' + asset, first, old[asset])
            self.expect_file(f'/_gregale/releases/{first}/index.html', first, old['index.html'])
            self.checked('retained_release_and_old_assets')
            self.api('POST', '/' + sid + '/promotions', {'release_id': first, 'expected_revision': first_revision},
                     status=409, code='hosting_release_conflict')
            self.checked('stale_promotion_rejected')
            rolled_back = self.accept_site(self.cli('sites', 'rollback', '--site', sid, '--release', first), sid)
            require(rolled_back.get('active_release_id') == first and rolled_back['revision'] > self.site['revision'],
                    'CLI rollback did not advance this site to the retained release')
            self.site = rolled_back
            self.checked('rollback', **self.expect_file('/', first, old['index.html']))
            self.check_usage_snapshot('after_rollback')
            if self.args.custom_domain:
                self.domain_contract('rollback', first, old)
                if self.args.check_domain_provider_outage:
                    self.domain_provider_outage(first, old)
            if self.site.get('durable_routing'):
                if self.args.edge_samples:
                    self.edge_observations(first, old['index.html'])
                if self.args.check_control_plane_outages:
                    self.control_plane_outages(first, old['index.html'])
                if self.args.check_publication_recovery:
                    self.publication_recovery(first, second, old['index.html'])
                self.check_suspension(first, old['index.html'])
            if self.args.check_worker_switch:
                self.check_switch(first, old['index.html'])
            self.cli('sites', 'retire', '--site', sid, '--release', second)
            def retired():
                status, _, body = self.edge(f'/_gregale/releases/{second}/index.html')
                return status == 404 and decode_json(body).get('code') == 'hosting_file_not_found'
            _, elapsed = self.wait('retired release to leave routing', retired)
            self.checked('retired_release_denied', observed_after_seconds=elapsed)
            self.check_usage_snapshot('after_retirement')
            if self.args.check_traffic:
                self.traffic_observations()
                if self.args.check_analytics_outage:
                    self.analytics_provider_outage(first, old)
            if self.args.custom_domain:
                self.remove_domain()

    def check_suspension(self, release, body):
        previous = self.site
        self.site = self.accept_site(self.cli('sites', 'suspend', '--site', previous['id']), previous['id'])
        require(self.site.get('suspended') is True and self.site['revision'] > previous['revision'],
                'CLI suspension did not commit a new site revision')
        def denied():
            status, headers, payload = self.edge()
            return status == 503 and 'x-gregale-release' not in headers and decode_json(payload).get('code') == 'hosting_route_unavailable'
        _, elapsed = self.wait('suspended site to stop serving', denied)
        self.checked('durable_site_suspended', observed_after_seconds=elapsed)
        if self.domain is not None:
            self.domain_denied_suspended()
        previous = self.site
        self.site = self.accept_site(self.cli('sites', 'resume', '--site', previous['id']), previous['id'])
        require(self.site.get('suspended') is False and self.site['revision'] > previous['revision'],
                'CLI resume did not commit a new site revision')
        self.checked('durable_site_resumed', **self.expect_file('/', release, body))

    def cleanup(self):
        if not self.site or not isinstance(self.site.get('id'), str) or not UUID.fullmatch(self.site['id']):
            return
        sid = self.site['id']
        self.report['cleanup'] = {'status': 'requested', 'site_id': sid, 'at': utc_now()}
        self.save()
        self.cli('sites', 'delete', '--site', sid)
        def revoked():
            status, headers, body = self.edge()
            return status == 503 and 'x-gregale-release' not in headers and decode_json(body).get('code') == 'hosting_route_unavailable'
        _, elapsed = self.wait('deleted site to stop serving', revoked)
        self.checked('deleted_site_denied', observed_after_seconds=elapsed)
        def gone():
            status, _, body = self.api_response('GET', '/' + sid)
            if status == 404:
                require(decode_json(body).get('code') == 'not_found', 'Site cleanup returned an unexpected error')
                return True
            require(status == 200, 'Site cleanup could not be observed through the API')
            return False
        _, elapsed = self.wait('artifact retirement and site removal', gone, self.args.cleanup_timeout)
        self.verify_removed_site(elapsed)

    def verify_removed_site(self, elapsed):
        sid = self.site['id']
        for prefix in ('staging', 'published'):
            require(self.storage_count(f'hosting/{prefix}/{sid}/') == 0, 'Retired fixture objects remain in private storage')
        if self.site['durable_routing']:
            head = self.storage_route_head()
            require(isinstance(head, dict) and head.get('version') == 2 and head.get('deleted') is True and
                    head.get('enabled') is False and head.get('expires_at') == 0 and
                    head.get('site_id') == sid and head.get('hostname') == self.site['hostname'] and
                    head.get('origin_id') == self.site['origin_id'] and
                    type(head.get('generation')) is int and 0 < head['generation'] < 2 ** 53 and
                    head.get('active_release_id') == '' and head.get('ready_releases') == [],
                    'Durable deletion did not retain a valid terminal routing head')
            self.checked('durable_deletion_head_retained', generation=head['generation'])
        self.report['cleanup']['status'] = 'passed'
        self.checked('artifact_and_metadata_cleanup', observed_after_seconds=elapsed)
        self.finish_acceptance_cleanup()

    def run(self):
        # Reserve the evidence destination before any external action. Existing
        # observations are never replaced by a new qualification run.
        fd = os.open(self.args.evidence, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        os.close(fd)
        self.save()
        try:
            self.preflight()
            if self.args.resource_profile:
                self.resource_snapshot('before')
            self.exercise()
            self.report['status'] = 'passed'
        except Failure as error:
            self.report['status'] = 'failed'
            self.report['failure'] = str(error)
        except KeyboardInterrupt:
            self.report['status'] = 'failed'
            self.report['failure'] = 'Qualification interrupted; cleanup was attempted'
        except (OSError, ValueError, KeyError, TypeError, AttributeError):
            self.report['status'] = 'failed'
            # Fixed text also covers errors from child tools that might contain secrets.
            self.report['failure'] = 'Qualification did not complete; inspect the last successful check and configured services'
        finally:
            try:
                self.restore_provider_fault()
            except (Failure, OSError, ValueError, KeyError, TypeError):
                self.report['status'] = 'failed'
                self.report['provider_fault']['status'] = 'restore_failed'
                self.report['provider_fault']['action'] = 'Restore the recorded provider fault with its scoped controller before deleting the site or clearing DNS'
            try:
                self.restore_outage()
            except (Failure, OSError, ValueError, KeyError, TypeError):
                self.report['status'] = 'failed'
                self.report['outage_recovery']['status'] = 'failed'
                self.report['outage_recovery']['action'] = 'Restore the recorded fault with the dedicated pilot controller before continuing'
            try:
                if self.provider_fault is not None:
                    self.report['cleanup'] = {'status': 'blocked', 'action': 'Confirm provider fault restoration before site/provider cleanup; preserve DNS'}
                else:
                    self.cleanup()
            except (Failure, OSError, ValueError, KeyError, TypeError):
                self.report['status'] = 'failed'
                self.report['cleanup'] = {'status': 'failed', 'site_id': self.site.get('id') if self.site else None,
                                          'action': 'Retire/delete the recorded qualification site and verify storage prefixes; preserve custom-domain DNS until provider cleanup is confirmed'}
            if self.args.resource_profile and self.report.get('resource_profile'):
                try:
                    self.resource_snapshot('after_cleanup')
                    self.finish_resource_profile()
                except (Failure, OSError, ValueError, KeyError, TypeError):
                    self.report['status'] = 'failed'
                    self.report['resource_profile']['status'] = 'failed'
            self.report['finished_at'] = utc_now()
            self.save()
        return self.report['status'] == 'passed'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--infrastructure', type=Path, required=True, help='Initialized, applied pilot Terraform directory')
    parser.add_argument('--terraform', default='terraform')
    parser.add_argument('--binary', default='gregale')
    parser.add_argument('--aws', default='aws', help='AWS CLI v2 used only for authenticated prefix inventory')
    parser.add_argument('--project', required=True, help='Existing disposable qualification project')
    parser.add_argument('--environment', required=True, help='Existing environment in that project')
    parser.add_argument('--account', required=True, help='Expected qualification account UUID')
    parser.add_argument('--evidence', type=Path, required=True, help='New private JSON report file')
    parser.add_argument('--edge-timeout', type=int, default=240)
    parser.add_argument('--cleanup-timeout', type=int, default=900, help='Must cover the nine-minute-thirty-second retirement grace')
    parser.add_argument('--check-worker-switch', action='store_true', help='Temporarily disable/restore this dedicated pilot Worker; affects every site on it')
    parser.add_argument('--outage-controller', type=Path, help='Absolute path to an operator-owned isolated-pilot fault controller')
    parser.add_argument('--resource-profile', action='store_true', help='Record dedicated apid CPU/RAM and hosting SQL relation bytes through the pilot controller')
    parser.add_argument('--check-control-plane-outages', action='store_true', help='Verify separate API and database isolation outages; requires a dedicated pilot')
    parser.add_argument('--check-publication-recovery', action='store_true', help='Verify pending/coalesced intent while only apid publication is blocked')
    parser.add_argument('--outage-duration', type=int, default=1200, help='Seconds per API/database fault, minimum twenty minutes')
    parser.add_argument('--outage-interval', type=int, default=30, help='Seconds between bounded warm/cold outage probes')
    parser.add_argument('--edge-samples', type=int, default=0, help='Up to 100 unique cold assets, each followed by a repeat request')
    parser.add_argument('--runner-region', default='', help='Operator-supplied runner location label; observed Cloudflare locations are recorded separately')
    parser.add_argument('--check-usage', action='store_true', help='Fill catalog reservations without uploads, check both account caps and cleanup; requires an empty, exclusive pilot account')
    parser.add_argument('--check-traffic', action='store_true', help='Wait for nonzero approximate provider observations; requires API and Worker analytics opt-ins')
    parser.add_argument('--custom-domain', default='', help='Disposable external CNAME subdomain, never reused after this run')
    parser.add_argument('--domain-controller', type=Path, help='Absolute isolated DNS/Access controller; required with --custom-domain')
    parser.add_argument('--provider-controller', type=Path, help='Absolute scoped provider-fault controller with independent watchdog restoration')
    parser.add_argument('--check-domain-recovery', action='store_true', help='Drop a successful create response and require an explicit run-bound provider-ID review')
    parser.add_argument('--domain-recovery-review', type=Path, help='New absolute JSON review file supplied by the operator after the recorded ambiguous create')
    parser.add_argument('--check-domain-provider-outage', action='store_true', help='Isolate only apid domain-provider management and verify serving/reconciliation recovery')
    parser.add_argument('--check-analytics-outage', action='store_true', help='Isolate only apid analytics queries, outlast its cache and verify reporting recovery')
    parser.add_argument('--provider-timeout', type=int, default=2400, help='Seconds to await provider reconciliation, including the active-domain 30-minute poll')
    parser.add_argument('--provider-observation-seconds', type=int, default=180, help='Seconds to observe an established provider fault, minimum three minutes')
    args = parser.parse_args()
    if args.edge_timeout <= 0 or args.cleanup_timeout <= 0:
        parser.error('timeouts must be positive')
    if not 1200 <= args.outage_duration <= 3600 or not 5 <= args.outage_interval <= 60:
        parser.error('outages must last 1200..3600 seconds with a 5..60 second probe interval')
    if not 0 <= args.edge_samples <= 100 or (args.edge_samples and re.fullmatch(r'[a-z0-9][a-z0-9-]{0,47}', args.runner_region) is None):
        parser.error('regional probes require 1..100 samples and a bounded runner-region label')
    try:
        def interrupted(signum, frame):
            raise KeyboardInterrupt
        signal.signal(signal.SIGTERM, interrupted)
        ok = Qualification(args).run()
    except (OSError, Failure):
        print('hosting qualification: cannot create evidence or initialize the configured tools')
        return 1
    print('hosting qualification: ' + ('PASSED' if ok else 'FAILED; see the private evidence report'))
    return 0 if ok else 1


if __name__ == '__main__':
    raise SystemExit(main())
