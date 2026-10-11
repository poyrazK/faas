#!/usr/bin/env python3
"""Exercise predeployed disposable profiling fixtures through the public API."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import subprocess
import time
import tempfile
import urllib.error
import urllib.parse
import urllib.request

MAX_BODY = 8 * 1024 * 1024


class HTTPFailure(RuntimeError):
    def __init__(self, status, path):
        self.status = status
        super().__init__(f'HTTP {status} from {path}')


def timestamp():
    return dt.datetime.now(dt.timezone.utc).isoformat().replace('+00:00', 'Z')


def request(url, body=None, token=None, timeout=30):
    headers = {'Accept': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    data = None if body is None else json.dumps(body).encode()
    if data is not None:
        headers['Content-Type'] = 'application/json'
    # Never forward API credentials through redirects or to fixture origins.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args):
            return None
    try:
        with urllib.request.build_opener(NoRedirect).open(
                urllib.request.Request(url, data=data, headers=headers), timeout=timeout) as response:
            raw = response.read(MAX_BODY + 1)
            if len(raw) > MAX_BODY:
                raise ValueError('response exceeds report bound')
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as exc:
        raise HTTPFailure(exc.code, urllib.parse.urlsplit(url).path) from None


def validate(config):
    for key in ('api_url',):
        if urllib.parse.urlsplit(config[key]).scheme not in ('http', 'https'):
            raise ValueError('API URL must use HTTP(S)')
    seconds = config.get('duration_seconds', 120)
    if not isinstance(seconds, int) or not 30 <= seconds <= 600:
        raise ValueError('duration_seconds must be 30–600')
    for mode in ('baseline', 'regression', 'label_loss', 'sparse'):
        fixture = config['fixtures'][mode]
        if urllib.parse.urlsplit(fixture['url']).scheme not in ('http', 'https'):
            raise ValueError('fixture URL must use HTTP(S)')
        if not fixture.get('deployment_id') or not fixture.get('slug'):
            raise ValueError('fixture slug and deployment_id are required')
    # Comparisons are app scoped. The operator must pin traffic to each deployment.
    if len({f['slug'] for f in config['fixtures'].values()}) != 1:
        raise ValueError('all fixture deployments must belong to the same app')
    ids = [f['deployment_id'] for f in config['fixtures'].values()]
    if len(set(ids)) != 4:
        raise ValueError('four distinct deployments are required')
    settle = config.get('settle_seconds', 15)
    if not isinstance(settle, int) or not 1 <= settle <= 120:
        raise ValueError('settle_seconds must be 1–120')
    if set(config['fixtures']) != {'baseline', 'regression', 'label_loss', 'sparse'}:
        raise ValueError('exactly four fixture modes are required')
    hook = config.get('native_restore_command')
    if hook is not None and (not isinstance(hook, list) or not hook or
                             not all(isinstance(x, str) for x in hook)):
        raise ValueError('native_restore_command must be an executable argument array')


class Runner:
    def __init__(self, config):
        self.config = config
        self.token = os.environ['GREGALE_ACCEPTANCE_TOKEN']
        self.root = config['api_url'].rstrip('/') + '/v1/apps/' + urllib.parse.quote(
            config['fixtures']['baseline']['slug'], safe='')
        self.report = {'started_at': timestamp(), 'windows': {}, 'checks': {},
                       'native_restore': {'status': 'not_run'}, 'status': 'incomplete'}

    def api(self, path, body=None):
        return request(self.root + path, body, self.token)

    def window(self, mode):
        self.report['stage'] = 'traffic:' + mode
        fixture = self.config['fixtures'][mode]
        url = fixture['url'].rstrip('/')
        health = request(url + '/readyz')
        if health.get('mode') != mode:
            raise ValueError(f'{mode}: fixture reports the wrong workload mode')
        start = timestamp()
        deadline = time.monotonic() + self.config.get('duration_seconds', 120)
        sent = 0
        # Sparse deliberately remains below the default minimum of 20 requests.
        while time.monotonic() < deadline:
            if mode != 'sparse' or sent < 5:
                request(url + '/hot/' + str(sent))
                sent += 1
            time.sleep(0.02 if mode != 'sparse' else 1)
        end = timestamp()
        q = {'deployment_id': fixture['deployment_id'], 'runtime': self.config.get('runtime', 'go124'),
             'start': start, 'end': end}
        self.report['windows'][mode] = {'query': q, 'successful_client_requests': sent}
        return q

    def profile(self, q):
        return self.api('/profiles?' + urllib.parse.urlencode(q))

    def check(self, mode, baseline, candidate):
        self.report['stage'] = 'assessment:' + mode
        saved = self.api('/profiles/investigations', {
            'expected_revision': 0, 'investigation': {
                'title': 'Profiling acceptance: ' + mode,
                'baseline': baseline, 'candidate': candidate}})
        options = {'metric': 'cpu_per_request', 'relative_increase_percent': 20,
                   'absolute_increase_cpu_per_second': 0.01,
                   'absolute_increase_cpu_seconds_per_request': 0.0001,
                   'minimum_profiles': 3, 'minimum_coverage_ratio': 0.8,
                   'minimum_requests': 20, 'routes': ['GET /hot/{id}']}
        result = self.api('/profiles/investigations/' + saved['saved']['id'] + '/check', {
            'expected_revision': saved['saved']['revision'], 'options': options})
        persisted = self.api('/profiles/investigations/' + saved['saved']['id'])
        assessment = result['saved']['assessment']
        persistence_ok = persisted['saved'].get('assessment') == assessment
        checks = [r for r in assessment.get('route_checks', []) if r['route'] == 'GET /hot/{id}']
        expected = 'regressed' if mode == 'regression' else 'insufficient_data'
        passed = persistence_ok and len(checks) == 1 and checks[0]['status'] == expected
        # Require the intended cause, rather than passing on unrelated missing data.
        if passed and mode == 'label_loss':
            labels = checks[0].get('label_coverage') or {}
            passed = labels.get('available', False) and not labels.get('consistent', True)
        if passed and mode == 'sparse':
            n = checks[0].get('candidate_requests')
            passed = isinstance(n, int) and 0 < n < 20
        self.report['checks'][mode] = {'passed': passed, 'expected': expected,
                                     'investigation_url': result['url'], 'persistence_verified': persistence_ok,
                                     'assessment': assessment}

    def run(self):
        windows = {mode: self.window(mode) for mode in self.config['fixtures']}
        time.sleep(self.config.get('settle_seconds', 15))
        for mode, q in windows.items():
            self.report['stage'] = 'profile:' + mode
            profile = self.profile(q)
            self.report['windows'][mode]['profile'] = profile
            rows = [r for r in profile.get('routes', []) if r['route'] == 'GET /hot/{id}']
            # Independent apid gateway telemetry, never the collector count itself.
            sent = self.report['windows'][mode]['successful_client_requests']
            window = self.report['windows'][mode]
            window['gateway_reconciled'] = (len(rows) == 1 and rows[0].get('requests') == sent)
            label = rows[0].get('label_coverage') or {} if rows else {}
            percent = label.get('percent')
            window['collector_reconciled'] = (label.get('available') is True and
                isinstance(percent, (int, float)) and
                (percent >= 80 if mode in ('baseline', 'regression') else
                 20 <= percent <= 60 if mode == 'label_loss' else True))
            filtered = self.profile(dict(q, route='GET /hot/{id}'))
            window['route_filter_verified'] = (len(rows) == 1 and
                abs(filtered['cpu_seconds'] - rows[0]['cpu_seconds']) <= 1e-8 and
                not any('[gregale-route]' in f.get('name', '')
                        for f in filtered.get('functions', [])))
            window['hotspot_present'] = any(
                f.get('name', '').endswith('.hotWork') for f in profile.get('functions', []))
        for mode in ('regression', 'label_loss', 'sparse'):
            self.check(mode, windows['baseline'], windows[mode])
        hook = self.config.get('native_restore_command')
        if hook:
            self.report['stage'] = 'native_restore'
            # Hook owns native host operations and writes a bounded JSON receipt.
            # Credentials stay in the inherited environment; command output is omitted.
            with tempfile.TemporaryFile() as output:
                completed = subprocess.run(hook, stdout=output, stderr=subprocess.DEVNULL,
                                           timeout=180, check=False)
                output.seek(0)
                raw = output.read(MAX_BODY + 1)
                if len(raw) > MAX_BODY:
                    raise ValueError('native restore receipt exceeds bound')
                receipt = json.loads(raw)
            native = {'receipt': receipt, 'status': 'failed'}
            self.report['native_restore'] = native
            if completed.returncode != 0:
                raise RuntimeError('native adapter failed; receipt retained')
            original = self.report['windows']['baseline']['profile']
            initial_window = self.report['windows']['baseline']
            after = self.window('baseline')
            self.report['windows']['post_restore'] = self.report['windows'].pop('baseline')
            self.report['windows']['baseline'] = initial_window
            time.sleep(self.config.get('settle_seconds', 15))
            before_profile = self.profile(windows['baseline'])
            after_profile = self.profile(after)
            native['post_restore_profile'] = after_profile
            post = self.report['windows']['post_restore']
            post['profile'] = after_profile
            post_rows = [r for r in after_profile.get('routes', []) if r['route'] == 'GET /hot/{id}']
            post['gateway_reconciled'] = (len(post_rows) == 1 and
                post_rows[0].get('requests') == post['successful_client_requests'])
            rows = [r for r in after_profile.get('routes', []) if r['route'] == 'GET /hot/{id}']
            passed = (receipt.get('restored_from_snapshot') is True and
                      receipt.get('epoch_before') and receipt.get('epoch_after') and
                      receipt['epoch_before'] != receipt['epoch_after'] and
                      receipt.get('stale_capture_rejected') is True and
                      before_profile['cpu_seconds'] == original['cpu_seconds'] and
                      before_profile.get('coverage', {}).get('received_profiles') ==
                      original.get('coverage', {}).get('received_profiles') and
                      len(rows) == 1 and (rows[0].get('label_coverage') or {}).get('available') is True and
                      ((rows[0].get('label_coverage') or {}).get('percent') or 0) >= 80)
            native['status'] = 'passed' if passed else 'failed'
        passed = (all(w.get('gateway_reconciled') for w in self.report['windows'].values()) and
                  all(w.get('collector_reconciled') and w.get('hotspot_present') and w.get('route_filter_verified')
                      for name, w in self.report['windows'].items() if name != 'post_restore') and
                  all(c['passed'] for c in self.report['checks'].values()))
        self.report['stage'] = 'completed'
        self.report['status'] = ('passed' if passed and self.report['native_restore']['status'] == 'passed'
                                 else 'incomplete' if passed and not hook else 'failed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    parser.add_argument('--report', required=True)
    args = parser.parse_args()
    runner = None
    try:
        config = json.loads(Path(args.config).read_text())
        validate(config)
        runner = Runner(config)
        runner.run()
    except Exception as exc:
        # Omit potentially sensitive exception text from networking/hook errors.
        report = runner.report if runner else {'status': 'failed'}
        report['status'] = 'failed'
        report['error_type'] = type(exc).__name__
    else:
        report = runner.report
    report['finished_at'] = timestamp()
    target = Path(args.report)
    target.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(report, output, indent=2)
    print('Profiling acceptance:', report['status'])
    return {'passed': 0, 'failed': 1, 'incomplete': 2}[report['status']]


if __name__ == '__main__':
    raise SystemExit(main())
