"""Scoped, opt-in provider failures; no automatic discovery or provider-ID adoption."""
import json
import math
import os
from pathlib import Path
import stat
import time
import uuid


MODES = ('create_response', 'domain_api', 'analytics_query')
CONTROLLER_ENV = ('PATH', 'HOME', 'USER', 'LANG', 'LC_ALL', 'SSH_AUTH_SOCK', 'HTTP_PROXY', 'HTTPS_PROXY',
                  'ALL_PROXY', 'NO_PROXY', 'http_proxy', 'https_proxy', 'all_proxy', 'no_proxy',
                  'SSL_CERT_FILE', 'SSL_CERT_DIR', 'REQUESTS_CA_BUNDLE', 'CURL_CA_BUNDLE')


class ProviderFaultChecks:
    def configure_provider_faults(self):
        self.provider_fault = None

    def provider_modes(self):
        return (['create_response'] if self.args.check_domain_recovery else []) + \
               (['domain_api'] if self.args.check_domain_provider_outage else []) + \
               (['analytics_query'] if self.args.check_analytics_outage else [])

    def prepare_provider_faults(self):
        require = self.probe_require
        args = self.args
        modes = self.provider_modes()
        require(bool(args.domain_recovery_review) == args.check_domain_recovery,
                'Reviewed recovery requires both its explicit scenario and a new operator review path')
        if not modes:
            return
        require(180 <= args.provider_observation_seconds <= 600 and 180 <= args.provider_timeout <= 3600,
                'Provider observation must last 180..600 seconds with a 180..3600 second timeout')
        require(not (args.check_domain_recovery or args.check_domain_provider_outage) or bool(args.custom_domain),
                'Domain provider faults require the disposable custom-domain scenario')
        require(not args.check_analytics_outage or args.check_traffic,
                'Analytics provider faults require the traffic observation scenario')
        require(self.deployment.get('durable_routing_enabled') is True, 'Provider faults require durable serving')
        require(args.provider_controller is not None, 'Provider faults require an explicit scoped controller')
        controller = Path(args.provider_controller)
        require(controller.is_absolute() and controller.is_file() and os.access(controller, os.X_OK),
                'The provider controller must be an absolute executable path')
        if args.check_domain_recovery:
            path = Path(args.domain_recovery_review)
            require(path.is_absolute() and path.parent.is_dir() and not path.exists() and not path.is_symlink() and
                    path != args.evidence, 'Recovery review must be a new absolute file distinct from evidence')
        value = self.provider_control('describe', self.provider_scope())
        supported = value.get('modes', [])
        require(value.get('dedicated') is True and isinstance(supported, list) and len(supported) <= len(MODES) and
                all(m in MODES for m in supported) and all(m in supported for m in modes),
                'Provider controller does not attest to the dedicated pilot and required scoped faults')

    def provider_scope(self, mode='', run_id='', duration=0):
        return {'version': 1, 'api_origin': self.api_url, 'account_id': self.args.account,
                'origin_id': self.deployment['origin_id'], 'worker_name': self.deployment['worker_name'],
                'site_id': (self.site or {}).get('id', ''), 'domain_id': (self.domain or {}).get('id', ''),
                'hostname': self.args.custom_domain, 'mode': mode, 'run_id': run_id, 'duration': duration}

    def provider_control(self, action, scope):
        result = self.probe_execute([str(self.args.provider_controller), action],
                                    {k: self.env[k] for k in CONTROLLER_ENV if k in self.env},
                                    input=json.dumps(scope).encode(), timeout=45)
        self.probe_require(len(result.stdout) <= 16384, 'Provider controller response exceeded its bound')
        value = self.probe_decode(result.stdout)
        self.probe_require(isinstance(value, dict) and all(type(value.get(k)) is type(v) and value[k] == v for k, v in scope.items()),
                           'Provider controller returned a foreign pilot, claim or fault identity')
        return value

    def enter_provider_fault(self, mode):
        self.probe_require(self.provider_fault is None and mode in MODES, 'Overlapping or unknown provider fault refused')
        duration = self.args.provider_timeout + self.args.provider_observation_seconds + 120
        scope = self.provider_scope(mode, str(uuid.uuid4()), duration)
        self.provider_fault = scope
        self.report['provider_fault'] = {**scope, 'status': 'armed'}
        self.report.setdefault('provider_faults', []).append(self.report['provider_fault'])
        self.save()  # A controller can apply a fault and lose its response.
        value = self.provider_control('enter', scope)
        until = value.get('automatic_restore_at')
        self.probe_require(type(until) in (int, float) and math.isfinite(until) and
                           time.time() + duration + 30 <= until <= time.time() + duration + 300,
                           'Provider fault lacks an independent bounded restoration watchdog')
        self.report['provider_fault'].update(status='active', automatic_restore_at=until)
        self.save()
        return scope

    def provider_status(self):
        self.probe_require(self.provider_fault is not None, 'Provider fault is not armed')
        value = self.provider_control('status', self.provider_fault)
        self.probe_require(value.get('fault_active') is True and value.get('api_running') is True,
                           'The scoped provider fault is not independently active with the API running')
        # An API/process outage or a management flag change cannot qualify.
        self.accept_site(self.api('GET', '/' + self.site['id']), self.site['id'])
        return value

    def restore_provider_fault(self):
        if self.provider_fault is None:
            return
        value = self.provider_control('leave', self.provider_fault)
        self.probe_require(value.get('fault_active') is False, 'Scoped provider fault restoration was not acknowledged')
        self.provider_fault = None
        self.report['provider_fault']['status'] = 'restored'
        self.save()

    def provider_receipt(self, scope, *, restored=False):
        value = self.provider_control('inspect', scope) if restored else self.provider_status()
        if restored:
            self.probe_require(value.get('fault_active') is False, 'Provider fault resumed after restoration')
        return value

    def exact_counter(self, value, key, expected=None):
        number = value.get(key)
        self.probe_require(type(number) is int and 0 <= number < 2 ** 53 and
                           (expected is None or number == expected), 'Provider fault evidence has invalid attempt or object counters')
        return number

    def single_create(self, value):
        for field in ('create_requests', 'provider_objects', 'dropped_responses'):
            self.exact_counter(value, field, 1)

    def review_scope(self, fault):
        return {k: fault[k] for k in ('version', 'api_origin', 'account_id', 'origin_id', 'worker_name',
                                      'site_id', 'domain_id', 'hostname', 'run_id')} | {
            'provider_origin_id': self.domain['provider_origin_id'], 'expected_revision': self.domain['revision']}

    def reviewed_provider(self, scope):
        path = Path(self.args.domain_recovery_review)
        if not path.exists() and not path.is_symlink():
            return False
        try:
            fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
            with os.fdopen(fd, 'rb') as stream:
                self.probe_require(stat.S_ISREG(os.fstat(stream.fileno()).st_mode), 'Recovery review must be a regular file')
                data = stream.read(8193)
        except OSError:
            raise self.probe_failure('Cannot read the explicit provider-ID review file') from None
        self.probe_require(len(data) <= 8192, 'Provider-ID review exceeded its bound')
        value = self.probe_decode(data)
        self.probe_require(isinstance(value, dict) and set(value) == set(scope) | {'provider_id'} and
                           all(type(value.get(k)) is type(v) and value[k] == v for k, v in scope.items()) and
                           isinstance(value.get('provider_id'), str) and self.probe_uuid.fullmatch(value['provider_id']) is not None,
                           'Provider-ID review does not match this run, claim and current revision')
        return value['provider_id']

    def ambiguous_domain_recovery(self, release, files):
        require = self.probe_require
        fault = None
        try:
            fault = self.enter_provider_fault('create_response')
            def conflicted(row):
                self.provider_status()
                require(not row['details'].get('provider_id'), 'Ambiguous create was implicitly bound before operator review')
                return row.get('last_error_code') == 'provider_conflict' and row['details'].get('create_started_at') and row
            row, _ = self.wait_domain('lost create response and durable conflict', conflicted, self.args.provider_timeout)
            marker, revision, initial_attempts = row['details']['create_started_at'], row['revision'], row.get('attempts')
            require(type(initial_attempts) is int and initial_attempts >= 1, 'Domain conflict lacks a completed reconciliation attempt')
            started = self.probe_clock()
            while True:
                self.single_create(self.provider_status())
                row = self.read_domain()
                require(not row['details'].get('provider_id') and row['details'].get('create_started_at') == marker and
                        row['revision'] == revision and row.get('last_error_code') == 'provider_conflict',
                        'Ambiguous domain state changed without reviewed recovery')
                self.provider_warm(release, files['index.html'])
                if self.probe_clock() - started >= self.args.provider_observation_seconds:
                    break
                self.probe_sleep(10)
            require(type(row.get('attempts')) is int and row['attempts'] > initial_attempts,
                    'No reconciliation retry occurred while duplicate creation was being observed')
        finally:
            self.restore_provider_fault()
        self.single_create(self.provider_receipt(fault, restored=True))
        scope = self.review_scope(fault)
        self.report['domain_recovery_review'] = {'status': 'required', 'identity': scope}
        self.save()
        print('hosting qualification: provider-ID review required; see the private evidence identity', flush=True)
        provider, _ = self.wait('explicit operator provider-ID review', lambda: self.reviewed_provider(scope), self.args.provider_timeout)
        current = self.read_domain()
        require(current['revision'] == scope['expected_revision'] and not current['details'].get('provider_id') and
                current['details'].get('create_started_at') == marker, 'Reviewed recovery claim changed; refresh and review separately')
        self.report['domain_recovery_review']['status'] = 'submitted'
        self.save()  # Never repeat a recovery POST after an uncertain response.
        try:
            status, _, body = self.api_response('POST', '/' + self.site['id'] + '/domains/' + self.domain['id'] + '/recover',
                                               {'provider_id': provider, 'expected_hostname': self.args.custom_domain,
                                                'expected_revision': scope['expected_revision']})
        except self.probe_failure:
            status, body = None, None
        if status is not None:
            require(status == 202, 'Reviewed provider-ID recovery was rejected; do not retry without a fresh review')
            self.accept_domain(self.probe_decode(body))
        def bound():
            row = self.read_domain()
            if not row['details'].get('provider_id'):
                return False
            require(row['details']['provider_id'] == provider and row['revision'] == scope['expected_revision'] + 1 and
                    row['details'].get('create_started_at') == marker, 'Recovery receipt differs from the reviewed provider or fencing revision')
            return True
        self.wait('single reviewed recovery receipt', bound)
        self.ready_domain()
        self.domain_contract('recovered', release, files)
        self.single_create(self.provider_receipt(fault, restored=True))
        self.report['domain_recovery_review']['status'] = 'completed'
        self.passed_acceptance('custom_domain_ambiguous_create_recovery', create_requests=1, provider_objects=1,
                               fault_run_id=fault['run_id'], observed_seconds=self.args.provider_observation_seconds, recovery_revision=self.domain['revision'])

    def provider_warm(self, release, body):
        for request in ([self.edge, self.domain_edge] if self.domain_token else [self.edge]):
            status, headers, actual = request()
            self.probe_require(status == 200 and actual == body and headers.get('x-gregale-release') == release,
                               'Provider failure interrupted canonical or custom-domain serving')
        self.denied()
        if self.domain_token:
            status, headers, _ = self.domain_edge(authenticated=False)
            self.probe_require(status in (302, 303, 401, 403) and 'x-gregale-release' not in headers and
                               'x-gregale-cache' not in headers, 'Provider failure bypassed custom-domain Access')

    def provider_cold(self, release, body, path, *, domain=False):
        request = self.domain_edge if domain else self.edge
        status, headers, actual = request(path)
        self.probe_require(status == 200 and actual == body and headers.get('x-gregale-release') == release and
                           headers.get('x-gregale-cache') == 'MISS', 'Provider failure did not preserve exact cold serving')

    def outage_files(self, mode, release, body):
        count = 2 * (math.ceil(self.args.provider_observation_seconds / 10) + 1)
        rid, files = self.cold_release(mode, count)
        self.await_edge_generation(release, body, self.await_publication())
        return rid, iter(files.items())

    def observe_provider_outage(self, release, files, rid, cold, condition):
        started = self.probe_clock()
        samples = 0
        while True:
            receipt = self.provider_status()
            self.probe_require(condition(), 'The observed provider failure disappeared before restoration')
            self.probe_require(self.exact_counter(receipt, 'failed_requests') >= 1, 'No scoped provider request actually failed')
            self.provider_warm(release, files['index.html'])
            name, body = next(cold)
            self.provider_cold(rid, body, f'/_gregale/releases/{rid}/{name}')
            if self.domain_token:
                name, body = next(cold)
                self.provider_cold(rid, body, f'/_gregale/releases/{rid}/{name}', domain=True)
            samples += 1
            if self.probe_clock() - started >= self.args.provider_observation_seconds:
                break
            self.probe_sleep(10)
        return samples

    def domain_provider_outage(self, release, files):
        require = self.probe_require
        original = self.read_domain()
        identity = {k: original['details'].get(k) for k in ('provider_id', 'access_id', 'access_aud')}
        require(all(identity.values()), 'Domain provider outage requires a bound active domain')
        initial_attempts = original.get('attempts')
        require(type(initial_attempts) is int and initial_attempts >= 1, 'Domain provider outage lacks a prior reconciliation receipt')
        rid, cold = self.outage_files('domain_api', release, files['index.html'])
        def failed():
            row = self.read_domain()
            require(row['state'] == 'active' and all(row['details'].get(k) == v for k, v in identity.items()) and
                    row['generation'] == original['generation'] and row['published_generation'] == original['published_generation'],
                    'Provider outage replaced or unpublished the existing alias')
            return row.get('last_error_code') == 'provider_unavailable' and type(row.get('attempts')) is int and row['attempts'] > initial_attempts
        fault = None
        try:
            fault = self.enter_provider_fault('domain_api')
            def established():
                self.provider_status()
                self.provider_warm(release, files['index.html'])
                return failed()
            self.wait('actual domain-provider failure at normal reconciliation', established, self.args.provider_timeout)
            samples = self.observe_provider_outage(release, files, rid, cold, failed)
            failed_attempts = self.domain['attempts']
        finally:
            self.restore_provider_fault()
        def recovered():
            row = self.read_domain()
            require(row['state'] == 'active' and all(row['details'].get(k) == v for k, v in identity.items()),
                    'Domain provider recovery changed the bound provider or Access application')
            return not row.get('last_error_code') and row.get('attempts', 0) > failed_attempts
        self.wait('successful domain-provider reconciliation after restore', recovered, self.args.provider_timeout)
        self.domain_contract('provider_restored', release, files)
        require(self.exact_counter(self.provider_receipt(fault, restored=True), 'successful_requests_after_restore') >= 1,
                'Domain provider restoration lacks a successful provider request')
        self.passed_acceptance('custom_domain_provider_outage', observed_seconds=self.args.provider_observation_seconds,
                               cold_samples=samples * 2, fault_run_id=fault['run_id'], bound_provider_preserved=True)

    def analytics_provider_outage(self, release, files):
        require = self.probe_require
        rid, cold = self.outage_files('analytics_query', release, files['index.html'])
        def unavailable():
            status, headers, body = self.api_response('GET', '/' + self.site['id'] + '/traffic')
            require(headers.get('cache-control') == 'no-store', 'Provider traffic response became cacheable')
            value = self.probe_decode(body)
            if status == 200:
                require(value.get('site_id') == self.site['id'] and value.get('approximate') is True and
                        value.get('source') == 'cloudflare_analytics_engine', 'Unexpected traffic report during query-cache expiry')
                return False  # A still-valid success cache cannot qualify an outage.
            require(status == 503 and value.get('code') == 'hosting_traffic_unavailable',
                    'Traffic failure was not a provider query error; flag/API outages cannot qualify')
            return True
        fault = None
        try:
            fault = self.enter_provider_fault('analytics_query')
            def established():
                self.provider_status()
                self.provider_warm(release, files['index.html'])
                return unavailable()
            self.wait('analytics success-cache expiry and actual query failure', established, self.args.provider_timeout)
            samples = self.observe_provider_outage(release, files, rid, cold, unavailable)
        finally:
            self.restore_provider_fault()
        # This validates the entire approximate report again, not just HTTP 200.
        self.traffic_observations()
        self.provider_warm(release, files['index.html'])
        require(self.exact_counter(self.provider_receipt(fault, restored=True), 'successful_requests_after_restore') >= 1,
                'Analytics recovery lacks a successful provider query after restoration')
        self.passed_acceptance('traffic_provider_outage', observed_seconds=self.args.provider_observation_seconds,
                               cold_samples=samples * (2 if self.domain_token else 1), fault_run_id=fault['run_id'], unavailable_was_not_zero=True)
