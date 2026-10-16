"""Bounded edge observations and an explicit, isolated-pilot outage protocol."""
import math
import os
from pathlib import Path
import re
import time
import uuid


def distribution(values):
    ordered = sorted(values)
    def percentile(fraction):
        return round(ordered[max(0, math.ceil(len(ordered) * fraction) - 1)], 3)
    return {'samples': len(ordered), 'min_ms': round(ordered[0], 3),
            'p50_ms': percentile(.5), 'p95_ms': percentile(.95), 'max_ms': round(ordered[-1], 3)}


class PilotProbes:
    # The host supplies fixed-diagnostic require/decode/execute helpers. Hooks
    # receive no API, Access, Cloudflare, R2, or AWS publication credentials.
    def configure_probes(self, require, decode, execute, failure):
        self.probe_require, self.probe_decode = require, decode
        self.probe_execute, self.probe_failure = execute, failure
        self.outage_active = None
        self.probe_clock, self.probe_sleep = time.monotonic, time.sleep

    def prepare_probes(self):
        require = self.probe_require
        args = self.args
        faults = args.check_control_plane_outages or args.check_publication_recovery
        enabled = faults or args.resource_profile
        require(not enabled or args.outage_controller is not None,
                'Outage/resource checks require an explicit isolated-pilot controller executable')
        require(not faults or self.deployment.get('durable_routing_enabled') is True,
                'Outage checks require durable Worker routing')
        if enabled:
            controller = Path(args.outage_controller)
            require(controller.is_absolute() and controller.is_file() and os.access(controller, os.X_OK),
                    'The outage controller must be an absolute executable path')
            self.controller_identity = self.control('describe')
            require(self.controller_identity.get('dedicated') is True,
                    'The outage controller must attest to a dedicated pilot control plane')
            required = (['api', 'database'] if args.check_control_plane_outages else [])
            required += ['publication'] if args.check_publication_recovery else []
            modes = self.controller_identity.get('modes', [])
            require(isinstance(modes, list) and len(modes) <= 3 and
                    all(mode in ('api', 'database', 'publication') for mode in modes) and all(mode in modes for mode in required),
                    'The outage controller does not support the requested fault modes')
            require(not args.resource_profile or 'resource_profile' in self.controller_identity.get('capabilities', []),
                    'The pilot controller cannot sample dedicated control-plane resources')

    def resource_snapshot(self, phase):
        value = self.control('snapshot')
        resources = value.get('resources', {})
        require = self.probe_require
        require(isinstance(resources, dict), 'Resource snapshot did not contain a resource object')
        require(type(value.get('sampled_at')) in (int, float) and 0 <= value['sampled_at'] < 2 ** 53 and abs(time.time() - value['sampled_at']) <= 60 and
                isinstance(value.get('apid_instance_id'), str) and re.fullmatch(r'[A-Za-z0-9_-]{1,128}', value['apid_instance_id']) is not None,
                'Resource snapshot is stale or lacks an apid process identity')
        cpu = resources.get('apid_cpu_seconds_total')
        require(type(cpu) in (int, float) and 0 <= cpu < 2 ** 53 and math.isfinite(cpu) and
                all(type(resources.get(name)) is int and 0 <= resources[name] < 2 ** 63
                    for name in ('apid_resident_memory_bytes', 'hosting_database_bytes')),
                'Resource snapshot contains invalid CPU, resident memory, or hosting relation bytes')
        samples = self.report.setdefault('resource_profile', {'status': 'running', 'samples': []})['samples']
        samples.append({'phase': phase, 'sampled_at': value['sampled_at'], 'apid_instance_id': value['apid_instance_id'],
                        **{name: resources[name] for name in ('apid_cpu_seconds_total', 'apid_resident_memory_bytes', 'hosting_database_bytes')}})
        self.save()

    def finish_resource_profile(self):
        profile = self.report['resource_profile']
        before, after = profile['samples'][0], profile['samples'][-1]
        self.probe_require(len(profile['samples']) >= 2, 'Resource profile needs before and after observations')
        profile['hosting_database_bytes_delta'] = after['hosting_database_bytes'] - before['hosting_database_bytes']
        if before['apid_instance_id'] == after['apid_instance_id'] and after['apid_cpu_seconds_total'] >= before['apid_cpu_seconds_total']:
            profile['apid_cpu_seconds_delta'] = after['apid_cpu_seconds_total'] - before['apid_cpu_seconds_total']
        else:
            self.report['not_run'].append('cross_restart_cpu_accounting')
        profile['status'] = 'passed'
        self.report['not_run'].remove('control_plane_resource_profile')
        self.checked('control_plane_resource_profile', samples=len(profile['samples']))

    def control(self, action, mode='', run_id='', duration=0):
        keys = ('PATH', 'HOME', 'USER', 'LANG', 'LC_ALL', 'SSH_AUTH_SOCK', 'HTTP_PROXY', 'HTTPS_PROXY',
                'ALL_PROXY', 'NO_PROXY', 'http_proxy', 'https_proxy', 'all_proxy', 'no_proxy',
                'SSL_CERT_FILE', 'SSL_CERT_DIR', 'REQUESTS_CA_BUNDLE', 'CURL_CA_BUNDLE')
        env = {key: self.env[key] for key in keys if key in self.env}
        command = [str(self.args.outage_controller), action]
        if mode:
            command += [mode, run_id, str(duration)]
        result = self.probe_execute(command, env, timeout=45)
        self.probe_require(len(result.stdout) <= 16384, 'Outage controller response exceeded its bound')
        value = self.probe_decode(result.stdout)
        require = self.probe_require
        require(isinstance(value, dict) and value.get('version') == 1 and
                value.get('api_origin') == self.api_url and value.get('account_id') == self.args.account and
                value.get('origin_id') == self.deployment['origin_id'] and
                value.get('worker_name') == self.deployment['worker_name'],
                'Outage controller identity differs from the configured disposable pilot')
        if mode:
            require(value.get('mode') == mode and value.get('run_id') == run_id,
                    'Outage controller returned a different fault identity')
        return value

    def enter_outage(self, mode, duration):
        run_id = str(uuid.uuid4())
        # Persist recovery identity before entering: a command can change the
        # host and lose its response. Leave is idempotent even in that case.
        self.outage_active = (mode, run_id, duration)
        self.report['outage_recovery'] = {'status': 'armed', 'mode': mode, 'run_id': run_id}
        self.save()
        value = self.control('enter', mode, run_id, duration)
        now = time.time()
        self.probe_require(type(value.get('automatic_restore_at')) in (int, float) and
                           now + duration + 30 <= value['automatic_restore_at'] <= now + duration + 300,
                           'Outage controller must arm a bounded independent automatic restoration')
        self.report['outage_recovery']['automatic_restore_at'] = value['automatic_restore_at']
        self.save()
        self.verify_outage()

    def verify_outage(self):
        mode, run_id, duration = self.outage_active
        value = self.control('status', mode, run_id, duration)
        self.probe_require(value.get('fault_active') is True and
                           (mode == 'api' or value.get('api_running') is True),
                           'The requested control-plane fault is not independently active')
        if mode in ('api', 'database'):
            try:
                status, _, _ = self.api_response('GET', '/' + self.site['id'], timeout=5)
            except self.probe_failure:
                self.probe_require(mode == 'api', 'Database isolation must leave the API reachable')
            else:
                self.probe_require(500 <= status < 600, 'A data-backed API read succeeded or failed for an unrelated reason during the outage')

    def restore_outage(self):
        if self.outage_active is None:
            return
        mode, run_id, duration = self.outage_active
        value = self.control('leave', mode, run_id, duration)
        self.probe_require(value.get('fault_active') is False, 'The pilot control-plane fault was not restored')
        self.outage_active = None
        self.report['outage_recovery']['status'] = 'fault_cleared'
        self.save()
        def recovered():
            try:
                status, _, body = self.api_response('GET', '/' + self.site['id'], timeout=5)
            except self.probe_failure:
                return False
            if status != 200:
                return False
            self.accept_site(self.probe_decode(body), self.site['id'])
            return True
        self.wait('pilot control plane to recover', recovered)
        self.report['outage_recovery']['status'] = 'restored'
        self.save()

    def publication(self):
        value = self.cli('sites', 'status', '--site', self.site['id'])
        self.accept_site(value.get('site'), self.site['id'])
        publication = value.get('publication', {})
        self.probe_require(publication.get('state') in ('pending', 'published') and
                           type(publication.get('generation')) is int and publication['generation'] > 0 and
                           type(publication.get('published_generation', 0)) is int and
                           0 <= publication.get('published_generation', 0) <= publication['generation'] and
                           (publication['state'] == 'published') == (publication.get('published_generation', 0) == publication['generation']),
                           'Publication status did not contain a valid generation pair')
        return publication

    def await_publication(self):
        def published():
            value = self.publication()
            return value if value['state'] == 'published' and value.get('published_generation') == value['generation'] else False
        publication, _ = self.wait('durable route publication receipt', published)
        return publication['generation']

    def probe_file(self, path, release, body, *, cold=False, minimum_generation=1):
        started = self.probe_clock()
        status, headers, actual = self.edge(path)
        elapsed = max(0, (self.probe_clock() - started) * 1000)
        require = self.probe_require
        require(status == 200 and actual == body and headers.get('x-gregale-release') == release,
                'An edge probe did not return the exact expected release bytes')
        generation = headers.get('x-gregale-route-generation', '')
        require(re.fullmatch(r'[1-9][0-9]{0,15}', generation) is not None and
                minimum_generation <= int(generation) < 2 ** 53,
                'The edge did not observe the required durable route generation')
        cache = headers.get('x-gregale-cache')
        require(cache in ('HIT', 'MISS') and (not cold or cache == 'MISS'),
                'A cold probe was already cached or did not report its actual cache result')
        colo = headers.get('cf-ray', '').rsplit('-', 1)[-1]
        require(re.fullmatch(r'[A-Z]{3}', colo) is not None, 'The edge probe did not report a Cloudflare location')
        return {'elapsed_ms': round(elapsed, 3), 'cache': cache, 'colo': colo, 'generation': int(generation)}

    def cold_release(self, label, count):
        files = {('index.html' if index == 0 else f'probe-{index}.html'): f'<!doctype html><title>{uuid.uuid4()} {label} {index}</title>'.encode()
                 for index in range(count)}
        request = {'spa_fallback': False, 'files': [
            {'path': name, 'sha256': self.probe_digest(body), 'size': len(body), 'content_type': 'text/html'}
            for name, body in files.items()]}
        suffix = '/' + self.site['id'] + '/releases'
        release = self.api('POST', suffix, request, status=201)['id']
        self.probe_require(isinstance(release, str) and self.probe_uuid.fullmatch(release), 'Cold probe release ID is invalid')
        suffix += '/' + release
        grants, granted_at = {}, 0
        for name, body in files.items():
            if not grants or self.probe_clock() - granted_at >= 120:
                grants = {grant['path']: grant for grant in self.api('POST', suffix + '/uploads')['uploads']}
                granted_at = self.probe_clock()
            self.upload(release, grants[name], body)
        self.probe_require(self.api('POST', suffix + '/finalize')['state'] == 'ready', 'Cold probe release was not finalized')
        return release, files

    def edge_observations(self, release, body):
        cold_release, files = self.cold_release('regional', self.args.edge_samples)
        generation = self.await_publication()
        self.await_edge_generation(release, body, generation)
        samples = []
        for name, content in files.items():
            path = f'/_gregale/releases/{cold_release}/{name}'
            samples.append(self.probe_file(path, cold_release, content, cold=True, minimum_generation=generation))
            samples.append(self.probe_file(path, cold_release, content, minimum_generation=generation))
        summary = {cache: distribution([s['elapsed_ms'] for s in samples if s['cache'] == cache])
                   for cache in ('HIT', 'MISS') if any(s['cache'] == cache for s in samples)}
        self.probe_require('HIT' in summary and 'MISS' in summary, 'Regional probes did not observe both cold and warm delivery')
        self.checked('regional_edge_observations', runner_region=self.args.runner_region,
                     locations=sorted({s['colo'] for s in samples}), by_cache=summary,
                     observations=samples, r2_file_reads_minimum=sum(s['cache'] == 'MISS' for s in samples))
        self.report['not_run'].remove('regional_latency_and_load')
        self.report['not_run'].append('regional_load')

    def await_edge_generation(self, release, body, generation):
        def observed():
            status, headers, actual = self.edge()
            text = headers.get('x-gregale-route-generation', '')
            return status == 200 and actual == body and headers.get('x-gregale-release') == release and text.isascii() and text.isdigit() and generation <= int(text) < 2 ** 53
        self.wait('edge to observe publication before fault injection', observed)

    def control_plane_outages(self, release, body):
        require = self.probe_require
        for mode in ('api', 'database'):
            count = math.ceil(self.args.outage_duration / self.args.outage_interval) + 1
            cold_release, files = self.cold_release(mode, count)
            generation = self.await_publication()
            self.await_edge_generation(release, body, generation)
            samples = []
            try:
                self.enter_outage(mode, self.args.outage_duration)
                started = self.probe_clock()
                for name, content in files.items():
                    self.verify_outage()
                    samples.append(self.probe_file('/', release, body, minimum_generation=generation))
                    samples.append(self.probe_file(f'/_gregale/releases/{cold_release}/{name}', cold_release, content,
                                                   cold=True, minimum_generation=generation))
                    if self.probe_clock() - started >= self.args.outage_duration:
                        break
                    self.probe_sleep(min(self.args.outage_interval, max(0, self.args.outage_duration - (self.probe_clock() - started))))
                require(self.probe_clock() - started >= self.args.outage_duration and any(s['cache'] == 'HIT' for s in samples),
                        'The complete outage duration and both cold/warm delivery were not observed')
                self.verify_outage()
                duration_observed = self.probe_clock() - started
            finally:
                self.restore_outage()
            self.probe_file('/', release, body, minimum_generation=generation)
            self.checked(mode + '_control_plane_outage', duration_seconds=round(duration_observed, 3),
                         locations=sorted({s['colo'] for s in samples}), observations=samples,
                         fault_run_id=self.report['outage_recovery']['run_id'], recovery_status=self.report['outage_recovery']['status'])
            self.report['not_run'].remove(mode + '_control_plane_outage')

    def publication_recovery(self, release, replacement, body):
        initial = self.await_publication()
        self.await_edge_generation(release, body, initial)
        try:
            self.enter_outage('publication', 2 * self.args.edge_timeout + 120)
            for target in (replacement, release):
                self.site = self.accept_site(self.api('POST', '/' + self.site['id'] + '/promotions',
                    {'release_id': target, 'expected_revision': self.site['revision']}, status=202), self.site['id'])
                def pending():
                    value = self.publication()
                    return value if value['state'] == 'pending' and value.get('last_error_code') == 'publication_unavailable' and value.get('retry_count', 0) > 0 else False
                last, _ = self.wait('durable publication failure and persistent retry', pending)
                self.verify_outage()
                self.probe_file('/', release, body, minimum_generation=initial)
        finally:
            self.restore_outage()
        generation = self.await_publication()
        self.probe_require(generation == last['generation'] and generation > initial,
                           'Publication recovery did not acknowledge the latest coalesced intent')
        self.await_edge_generation(release, body, generation)
        self.checked('publication_retry_recovery', generation=generation, superseded_generation=initial,
                     fault_run_id=self.report['outage_recovery']['run_id'], recovery_status=self.report['outage_recovery']['status'])
        self.report['not_run'].remove('publication_retry_recovery')
