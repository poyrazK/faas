"""Opt-in hosting acceptance checks; all destinations belong to an explicit pilot."""
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import re
import tempfile
import time
import uuid


COUNTERS = ('requests', 'payload_bytes', 'cache_hits', 'cache_misses', 'client_errors', 'server_errors')
USAGE = ('reserved_bytes', 'payload_bytes', 'ready_payload_bytes', 'retiring_reserved_bytes', 'pending_uploads', 'releases')


class AcceptanceChecks:
    def configure_acceptance(self):
        self.domain = None
        self.domain_token = None
        self.domain_token_read_at = 0
        self.domain_dns_dirty = False
        self.domain_run = str(uuid.uuid4())

    def passed_acceptance(self, name, **evidence):
        self.checked(name, **evidence)
        if name in self.report['not_run']:
            self.report['not_run'].remove(name)
        self.save()

    def account_json(self, suffix):
        status, headers, body = self.api_response('GET', suffix, root='/v1/internal/hosting')
        self.probe_require(status == 200 and headers.get('cache-control') == 'no-store',
                           'Account hosting observation failed or was cacheable')
        value = self.probe_decode(body)
        self.probe_require(isinstance(value, dict), 'Invalid account hosting observation')
        return value

    def prepare_acceptance(self):
        require = self.probe_require
        args = self.args
        require(bool(args.custom_domain) == bool(args.domain_controller),
                'Custom-domain checks require both a disposable hostname and its explicit controller')
        if not (args.check_usage or args.check_traffic or args.custom_domain):
            return
        self.capabilities = self.account_json('/capabilities')
        if args.check_usage:
            for field in ('account_storage_max_bytes', 'account_pending_uploads_max', 'release_max_bytes',
                          'file_max_bytes', 'manifest_max_bytes', 'releases_per_site'):
                require(type(self.capabilities.get(field)) is int and self.capabilities[field] > 0,
                        'Missing account admission capability')
            self.usage_baseline = self.usage()
            require(all(self.usage_baseline[k] == 0 for k in USAGE),
                    'Quota acceptance requires an empty and exclusively owned disposable account')
            reserved = self.reservation(self.capabilities['release_max_bytes'])
            count = self.capabilities['account_storage_max_bytes'] // reserved
            require(1 <= self.capabilities['account_pending_uploads_max'] <= count <
                    min(9, self.capabilities['releases_per_site']),
                    'Current quota capabilities exceed this bounded acceptance scenario')
            require(self.capabilities['account_pending_uploads_max'] < count and
                    self.capabilities['release_max_bytes'] // self.capabilities['file_max_bytes'] < 1000,
                    'Quota fixture cannot isolate both account limits within its file bound')
        if args.check_traffic:
            require(self.capabilities.get('analytics_enabled') is True and
                    self.deployment.get('analytics_configured') is True and self.deployment.get('analytics_enabled') is True,
                    'Traffic checks require independently enabled API and Worker analytics')
        if args.custom_domain:
            host = args.custom_domain
            require(isinstance(host, str) and len(host) <= 64 and
                    re.fullmatch(r'(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.){2,}[a-z]{2,}', host) is not None and
                    self.capabilities.get('domains_enabled') is True and
                    self.deployment.get('custom_domains_configured') is True and
                    self.deployment.get('custom_domains_enabled') is True and
                    self.deployment.get('durable_routing_enabled') is True,
                    'Custom-domain checks require an external subdomain and enabled durable/domain switches')
            for platform in (self.deployment['hosting_domain'], self.api_url.split('://')[1]):
                require(host != platform and not host.endswith('.' + platform) and not platform.endswith('.' + host),
                        'Qualification domain overlaps a platform origin')
            target = 'customers.' + self.deployment['hosting_domain']
            require(self.deployment.get('domain_cname_target') == target and
                    self.capabilities.get('domain_cname_target') == target, 'Domain CNAME target differs from the pilot')
            controller = Path(args.domain_controller)
            require(controller.is_absolute() and controller.is_file() and os.access(controller, os.X_OK),
                    'The domain controller must be an absolute executable path')
            value = self.domain_control('describe')
            require(value.get('dedicated') is True and value.get('disposable_hostname') is True,
                    'Domain controller must attest to an exclusively owned, unused disposable hostname')
            self.report['custom_domain'] = {'hostname': host, 'run_id': self.domain_run, 'status': 'prepared'}
            self.save()

    def reservation(self, payload):
        # Matches the documented two-copy catalog contract, not physical R2 usage.
        return 2 * payload + self.capabilities['manifest_max_bytes']

    def usage(self):
        value = self.account_json('/usage')
        require = self.probe_require
        require(value.get('account_id') == self.args.account and
                all(type(value.get(k)) is int and 0 <= value[k] < 2 ** 53 for k in USAGE) and
                value.get('storage_limit_bytes') == self.capabilities['account_storage_max_bytes'] and
                value.get('pending_uploads_limit') == self.capabilities['account_pending_uploads_max'],
                'Account usage identity, counters, or limits changed')
        return value

    def check_usage_snapshot(self, phase):
        if not self.args.check_usage:
            return
        require = self.probe_require
        def consistent():
            rows = self.api('GET', '/' + self.site['id'] + '/releases')
            require(isinstance(rows, list), 'Expected the pilot release catalog')
            expected = dict.fromkeys(USAGE, 0)
            for row in rows:
                require(row.get('site_id') == self.site['id'] and row.get('state') in ('pending', 'ready', 'retiring') and
                        type(row.get('artifact_bytes')) is int and row['artifact_bytes'] >= 0,
                        'Invalid release accounting record')
                size = sum(file['size'] for file in row['manifest']['files'])
                require(size == row['artifact_bytes'], 'Release accounting disagrees with its manifest')
                expected['releases'] += 1
                expected['payload_bytes'] += size
                expected['reserved_bytes'] += self.reservation(size)
                if row['state'] == 'pending':
                    expected['pending_uploads'] += 1
                elif row['state'] == 'ready':
                    expected['ready_payload_bytes'] += size
                else:
                    expected['retiring_reserved_bytes'] += self.reservation(size)
            observed = self.usage()
            return observed if all(observed[k] == expected[k] for k in USAGE) else False
        observed, _ = self.wait('consistent pilot catalog and account accounting', consistent)
        self.checked('usage_' + phase, **{k: observed[k] for k in USAGE})
        return observed

    def quota_admission(self):
        require = self.probe_require
        cap = self.capabilities
        remaining = cap['release_max_bytes']
        files = []
        while remaining:
            size = min(remaining, cap['file_max_bytes'])
            files.append({'path': 'index.html' if not files else f'quota-{len(files)}.bin', 'sha256': '0' * 64,
                          'size': size, 'content_type': 'application/octet-stream'})
            remaining -= size
        large = {'files': files, 'spa_fallback': False}
        base = '/' + self.site['id'] + '/releases'
        pending = []
        accepted = []
        def rejected(value, code, limit, observed):
            problem = self.api('POST', base, value, status=409, code=code)
            require(problem.get('limit') == limit and problem.get('observed') == observed and
                    isinstance(problem.get('docs_url'), str) and problem['docs_url'].startswith('https://'),
                    'Quota rejection omitted its limit, attempted value, or documentation')
        usage = self.usage()
        while usage['reserved_bytes'] + self.reservation(cap['release_max_bytes']) <= cap['account_storage_max_bytes']:
            require(len(accepted) < 8, 'Quota fixture exceeded its bounded catalog size')
            if len(pending) == cap['account_pending_uploads_max']:
                # A zero-payload manifest isolates pending admission from byte admission.
                small = {'files': [{**files[0], 'size': 0}], 'spa_fallback': False}
                rejected(small, 'hosting_pending_upload_quota_exceeded', cap['account_pending_uploads_max'], len(pending) + 1)
                self.checked('pending_upload_quota_rejected')
                self.api('DELETE', base + '/' + pending.pop(0), status=202)
                retired = self.usage()
                require(retired['reserved_bytes'] == usage['reserved_bytes'] and
                        retired['pending_uploads'] == usage['pending_uploads'] - 1,
                        'Retirement released bytes before artifact/catalog cleanup')
                usage = retired
            release = self.api('POST', base, large, status=201)
            require(self.probe_uuid.fullmatch(release.get('id', '')) is not None,
                    'Invalid quota fixture release identity')
            require(release['id'] not in accepted, 'Quota creation reused an existing release identity')
            pending.append(release['id'])
            accepted.append(release['id'])
            usage = self.check_usage_snapshot('quota_reservation_' + str(len(accepted)))
        rejected(large, 'hosting_storage_quota_exceeded', cap['account_storage_max_bytes'],
                 usage['reserved_bytes'] + self.reservation(cap['release_max_bytes']))
        self.passed_acceptance('account_quota_admission', reserved_bytes=usage['reserved_bytes'],
                               retained_releases=len(accepted), uploaded_bytes=0)
        for rid in pending:
            self.api('DELETE', base + '/' + rid, status=202)
        self.check_usage_snapshot('quota_retiring')
        self.wait('quota fixture catalog cleanup', lambda: self.usage()['releases'] == 0, self.args.cleanup_timeout)
        self.check_usage_snapshot('quota_reclaimed')

    def access_contract(self, release, files):
        # Cold paths must really miss; query-string changes do not bypass the file cache.
        path = '/help/'
        etag = '"sha256:' + self.probe_digest(files['help/index.html']) + '"'
        variants = [('GET', {}), ('HEAD', {}), ('GET', {'If-None-Match': etag})]
        for method, headers in variants:
            self.denied(path, method, headers)
        status, headers, body = self.edge(path)
        self.probe_require(status == 200 and headers.get('x-gregale-release') == release and
                           headers.get('x-gregale-cache') == 'MISS' and body == files['help/index.html'],
                           'Authorized cold Access fixture did not miss with the expected bytes')
        self.expect_file(path, release, files['help/index.html'], cache_hit=True)
        for method, headers in variants:
            self.denied(path, method, headers)
        self.checked('access_cold_warm_head_conditional')

    def domain_control(self, action, records=None):
        # Only the explicit controller may manage customer DNS or mint its own
        # Access identity. Runner credentials never enter its environment/input.
        keys = ('PATH', 'HOME', 'USER', 'LANG', 'LC_ALL', 'SSH_AUTH_SOCK', 'HTTP_PROXY', 'HTTPS_PROXY',
                'ALL_PROXY', 'NO_PROXY', 'http_proxy', 'https_proxy', 'all_proxy', 'no_proxy',
                'SSL_CERT_FILE', 'SSL_CERT_DIR', 'REQUESTS_CA_BUNDLE', 'CURL_CA_BUNDLE')
        identity = {'version': 1, 'api_origin': self.api_url, 'account_id': self.args.account,
                    'origin_id': self.deployment['origin_id'], 'worker_name': self.deployment['worker_name'],
                    'hostname': self.args.custom_domain, 'run_id': self.domain_run}
        request = {**identity, 'site_id': (self.site or {}).get('id', ''), 'domain_id': (self.domain or {}).get('id', ''),
                   'records': records or [], 'access_aud': (self.domain or {}).get('details', {}).get('access_aud', '')}
        result = self.probe_execute([str(self.args.domain_controller), action],
                                    {k: self.env[k] for k in keys if k in self.env}, timeout=45,
                                    input=json.dumps(request).encode())
        self.probe_require(len(result.stdout) <= 16384, 'Domain controller response exceeded its bound')
        value = self.probe_decode(result.stdout)
        self.probe_require(isinstance(value, dict) and all(value.get(k) == v for k, v in identity.items()),
                           'Domain controller identity differs from the configured disposable pilot')
        return value

    def accept_domain(self, row):
        require = self.probe_require
        require(isinstance(row, dict) and self.probe_uuid.fullmatch(row.get('id', '')) is not None and
                row.get('site_id') == self.site['id'] and row.get('account_id') == self.args.account and
                row.get('hostname') == self.args.custom_domain and
                isinstance(row.get('provider_origin_id'), str) and
                re.fullmatch(r'[0-9a-f]{64}', row['provider_origin_id']) is not None and
                type(row.get('revision')) is int and row['revision'] > 0 and
                (self.domain is None or all(row.get(k) == self.domain.get(k) for k in
                                           ('id', 'provider_origin_id', 'challenge'))),
                'Custom-domain response changed the pilot claim identity')
        return row

    def create_domain(self):
        self.domain = self.accept_domain(self.api('POST', '/' + self.site['id'] + '/domains',
                                                 {'hostname': self.args.custom_domain}, status=202))
        self.report['custom_domain'].update(id=self.domain['id'], status='claimed')
        self.save()

    def wait_domain(self, name, predicate, timeout=None):
        require = self.probe_require
        last_records = None
        def ready():
            nonlocal last_records
            rows = self.api('GET', '/' + self.site['id'] + '/domains')
            row = next((r for r in rows if r.get('id') == self.domain['id']), None)
            self.domain = self.accept_domain(row)
            challenge = row.get('challenge', '')
            require(re.fullmatch(r'[0-9a-f]{64}', challenge) is not None, 'Invalid domain proof challenge')
            records = [{'type': 'TXT', 'name': '_gregale-hosting.' + self.args.custom_domain, 'value': 'gregale-hosting=' + challenge},
                       {'type': 'CNAME', 'name': self.args.custom_domain, 'value': self.capabilities['domain_cname_target']}]
            extra = row.get('details', {}).get('records', [])
            require(isinstance(extra, list) and len(extra) <= 16, 'Domain validation records exceeded their bound')
            for r in extra:
                name, value = r.get('name', ''), r.get('value', '')
                require(r.get('type') in ('TXT', 'CNAME') and isinstance(name, str) and len(name) <= 253 and
                        re.fullmatch(r'[a-zA-Z0-9_.-]+', name) is not None and
                        (name == self.args.custom_domain or name.endswith('.' + self.args.custom_domain)) and
                        isinstance(value, str) and 0 < len(value) <= 2048 and '\n' not in value and '\r' not in value,
                        'Domain validation record escaped the disposable hostname')
                records.append({k: r[k] for k in ('type', 'name', 'value')})
            if records != last_records:
                self.domain_dns_dirty = True
                self.report['custom_domain']['dns_cleanup'] = 'required'
                self.save()  # Arm cleanup before a DNS command can lose its response.
                require(self.domain_control('sync', records).get('applied') is True, 'Domain DNS synchronization was not acknowledged')
                last_records = records
            return predicate(row)
        return self.wait(name, ready, timeout)

    def read_domain(self):
        rows = self.api('GET', '/' + self.site['id'] + '/domains')
        self.domain = self.accept_domain(next((r for r in rows if r.get('id') == self.domain['id']), None))
        return self.domain

    def ready_domain(self):
        require = self.probe_require
        def ready(row):
            require(row.get('last_error_code') != 'provider_conflict',
                    'Ambiguous domain creation requires explicit reviewed provider-ID recovery; automatic adoption is refused')
            return row.get('state') == 'active' and row.get('details', {}).get('tls_status') == 'active' and \
                re.fullmatch(r'[0-9a-f]{64}', row.get('details', {}).get('access_aud', '')) is not None and \
                type(row.get('generation')) is int and row['generation'] > 0 and row.get('published_generation') == row['generation']
        self.wait_domain('custom-domain DNS, TLS, Access and publication', ready)
        token = self.domain_control('token').get('access_token', '')
        require(isinstance(token, str) and re.fullmatch(r'[A-Za-z0-9_.-]{1,16384}', token) is not None,
                'The dedicated domain controller did not supply an Access JWT')
        self.domain_token = token
        self.domain_token_read_at = time.monotonic()
        self.checked('custom_domain_ready')

    def domain_edge(self, path='/', *, method='GET', authenticated=True, headers=None):
        values = dict(headers or {})
        if authenticated:
            if time.monotonic() - self.domain_token_read_at >= 60:
                token = self.domain_control('token').get('access_token', '')
                self.probe_require(isinstance(token, str) and re.fullmatch(r'[A-Za-z0-9_.-]{1,16384}', token) is not None,
                                   'Domain Access refresh did not supply an identity token')
                self.domain_token, self.domain_token_read_at = token, time.monotonic()
            values['Cookie'] = 'CF_Authorization=' + self.domain_token
        return self.http.request(method, 'https://' + self.args.custom_domain + path, values)

    def domain_contract(self, phase, release, files):
        require = self.probe_require
        etag = '"sha256:' + self.probe_digest(files['index.html']) + '"'
        def access_denied():
            # Canonical cookies must not authorize a custom-domain audience.
            for method, headers in [('GET', {}), ('HEAD', {}), ('GET', {'If-None-Match': etag}),
                                    ('GET', {'Cookie': 'CF_Authorization=' + self.access_token()})]:
                status, received, _ = self.domain_edge(method=method, authenticated=False, headers=headers)
                require(status in (302, 303, 401, 403) and 'x-gregale-release' not in received and
                        'x-gregale-cache' not in received,
                        'Custom-domain Access accepted an unauthenticated or canonical-audience request')
        access_denied()
        def serving():
            status, headers, body = self.domain_edge()
            if status != 200 or headers.get('x-gregale-release') != release:
                return False
            require(body == files['index.html'], 'Custom domain served incorrect fixture bytes')
            require(headers.get('etag') == etag and headers.get('cache-control') == 'private, max-age=0, must-revalidate',
                    'Custom-domain cache policy or ETag changed')
            return headers.get('x-gregale-cache') == 'HIT'
        self.wait('custom-domain release', serving)
        access_denied()
        status, headers, body = self.domain_edge(method='HEAD')
        require(status == 200 and not body and headers.get('x-gregale-release') == release, 'Custom-domain HEAD failed')
        status, _, body = self.domain_edge(headers={'If-None-Match': etag})
        require(status == 304 and not body, 'Custom-domain conditional request failed')
        self.checked('custom_domain_' + phase)

    def domain_denied_suspended(self):
        def denied():
            status, headers, _ = self.domain_edge()
            return status == 503 and 'x-gregale-release' not in headers
        self.wait('custom-domain suspension', denied)
        self.checked('custom_domain_suspended')

    def remove_domain(self):
        suffix = '/' + self.site['id'] + '/domains'
        rows = self.api('GET', suffix)
        self.domain = self.accept_domain(next((r for r in rows if r.get('id') == self.domain['id']), None))
        self.api('DELETE', suffix + '/' + self.domain['id'], {'expected_revision': self.domain['revision']}, status=202)
        # Observe revocation during the tombstone grace, while the certificate
        # and DNS still exist. Provider removal can later make TLS unavailable.
        def revoked():
            status, headers, _ = self.domain_edge()
            return status in (302, 303, 401, 403, 404, 503) and 'x-gregale-release' not in headers
        self.wait('custom-domain cached content revocation', revoked)
        self.checked('custom_domain_cached_content_revoked')
        self.wait('custom-domain safe removal', lambda: not self.api('GET', suffix), self.args.cleanup_timeout)
        self.finish_domain_cleanup()
        self.passed_acceptance('custom_domain_lifecycle')

    def finish_domain_cleanup(self):
        require = self.probe_require
        with tempfile.TemporaryDirectory(prefix='gregale-hosting-domain-') as directory:
            path = Path(directory) / 'head.json'
            self.storage('get-object', '--bucket', self.deployment['bucket'], '--key',
                         'hosting/domains/' + self.args.custom_domain + '.json', '--range', 'bytes=0-8192', str(path))
            body = path.read_bytes()
            require(len(body) <= 8192, 'Domain terminal head exceeded its bound')
            head = self.probe_decode(body)
        require(isinstance(head, dict) and head.get('version') == 1 and head.get('deleted') is True and
                head.get('enabled') is False and head.get('id') == self.domain['id'] and
                head.get('site_id') == self.site['id'] and head.get('hostname') == self.args.custom_domain and
                head.get('canonical_hostname') == self.site['hostname'] and head.get('origin_id') == self.site['origin_id'] and
                type(head.get('generation')) is int and 0 < head['generation'] < 2 ** 53,
                'Custom-domain cleanup did not retain the owned terminal head')
        if self.domain_dns_dirty:
            require(self.domain_control('clear').get('cleared') is True, 'Domain DNS cleanup was not acknowledged')
            self.domain_dns_dirty = False
        self.report['custom_domain'].update(status='removed', dns_cleanup='passed')
        self.save()

    def finish_acceptance_cleanup(self):
        if self.domain is not None and self.report['custom_domain'].get('status') != 'removed':
            self.finish_domain_cleanup()
        if self.args.check_usage:
            observed = self.usage()
            self.probe_require(all(observed[k] == 0 for k in USAGE), 'Account reservations survived fixture cleanup')
            self.passed_acceptance('account_reservation_cleanup', reserved_bytes=0, pending_uploads=0)

    def traffic_observations(self):
        require = self.probe_require
        def timestamp(value):
            require(isinstance(value, str), 'Missing traffic observation timestamp')
            result = datetime.fromisoformat(value.replace('Z', '+00:00'))
            require(result.utcoffset() == timedelta(0), 'Traffic observation timestamp is not UTC')
            return result
        def counters(value):
            require(all(type(value.get(k)) is int and 0 <= value[k] < 2 ** 53 for k in COUNTERS),
                    'Invalid approximate traffic counter')
            require(value['cache_hits'] + value['cache_misses'] <= value['requests'] and
                    value['client_errors'] + value['server_errors'] <= value['requests'],
                    'Traffic subcounts exceed observed requests')
        def observed():
            status, headers, body = self.api_response('GET', '/' + self.site['id'] + '/traffic')
            require(status == 200 and headers.get('cache-control') == 'no-store',
                    'Traffic observations unavailable; provider failures must not qualify as zero traffic')
            value = self.probe_decode(body)
            require(isinstance(value, dict) and value.get('site_id') == self.site['id'] and
                    value.get('source') == 'cloudflare_analytics_engine' and value.get('approximate') is True and
                    type(value.get('max_sample_interval')) is int and 0 <= value['max_sample_interval'] < 2 ** 53,
                    'Traffic observations lack the owned site, approximate source, or sampling weight')
            counters(value)
            require(value['requests'] == 0 or value['max_sample_interval'] >= 1, 'Nonzero traffic omitted its sampling weight')
            start, end, sampled = (timestamp(value[k]) for k in ('period_start', 'period_end', 'observed_at'))
            require(end - start == timedelta(hours=24) and abs((datetime.now(timezone.utc) - sampled).total_seconds()) <= 180 and
                    end.second == 0 and end.microsecond == 0 and 0 <= (sampled - end).total_seconds() < 120,
                    'Traffic observation window is stale or invalid')
            buckets, failures = value.get('buckets'), value.get('failures')
            require(isinstance(buckets, list) and 24 <= len(buckets) <= 25 and
                    isinstance(failures, list) and len(failures) <= 20, 'Traffic report has invalid bounded groups')
            previous = None
            for bucket in buckets:
                counters(bucket)
                at = timestamp(bucket['start'])
                require(at.minute == 0 and at.second == 0 and at.microsecond == 0 and
                        at < end and at + timedelta(hours=1) > start and
                        (previous is None or at - previous == timedelta(hours=1)), 'Traffic hourly buckets are unordered or out of window')
                previous = at
            require(all(sum(b[k] for b in buckets) == value[k] for k in COUNTERS), 'Traffic buckets do not sum to report totals')
            allowed = {'method_not_allowed': 405, 'hosting_file_not_found': 404, 'hosting_route_unavailable': 503,
                       'hosting_unavailable': 503, 'hosting_artifact_unavailable': 503}
            for failure in failures:
                require(failure.get('code') in allowed and failure.get('status') == allowed[failure['code']] and
                        type(failure.get('requests')) is int and 0 < failure['requests'] <= value['requests'] and
                        timestamp(failure['start']) in [timestamp(b['start']) for b in buckets], 'Invalid fixed traffic failure group')
            if value['requests'] == 0:
                return False  # Empty observations do not prove collection is functioning.
            return {k: value[k] for k in (*COUNTERS, 'max_sample_interval')}
        value, elapsed = self.wait('nonzero provider traffic observations', observed)
        self.passed_acceptance('traffic_observations', approximate=True, observed_after_seconds=elapsed, **value)
