"""Private Unix-socket Cloudflare relay and independently running fault watchdog."""
import http.client
import fcntl
import stat
from datetime import datetime, timedelta
from http.server import BaseHTTPRequestHandler
import json
import os
import re
from socketserver import ThreadingMixIn, UnixStreamServer
import threading
import time
from urllib.parse import parse_qs, urlsplit

from internal_frontend_hosting_pilot_common import HTTPS, PilotError, atomic_json, hex_id, identifier, need


MODES = ('create_response', 'domain_api', 'analytics_query')


def expire(state, now=None):
    now = time.time() if now is None else now
    active = state.get('active')
    if active and state['faults'][active]['restore_at'] <= now:
        state['faults'][active]['restored_at'] = now
        state['active'] = None


def local_health(root):
    import socket
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(3)
    connection = http.client.HTTPConnection('hosting-pilot.local', timeout=3)
    try:
        sock.connect(str(root / 'relay.sock'))
        connection.sock = sock
        connection.request('GET', '/__pilot_health')
        reply = connection.getresponse()
        need(reply.status == 200)
        value = json.loads(reply.read(16385))
        need(value.get('ready') is True and value.get('pid') is not None, 'Pilot relay is not ready')
        return value
    finally:
        connection.close()
        sock.close()


class ProviderController:
    def __init__(self, journal, http=None, health=None):
        self.journal, self.http = journal, http or HTTPS()
        self.health = health or (lambda: local_health(journal.root))

    def run(self, action, scope):
        if scope.get('hostname') == '':
            need(scope.get('mode') in ('', 'analytics_query') and scope.get('domain_id') == '')
            self.journal.identity({**scope, 'hostname': self.journal.config['hostname']})
        else:
            self.journal.identity(scope)
        fields = {'version', 'api_origin', 'account_id', 'origin_id', 'worker_name', 'hostname', 'site_id', 'domain_id', 'mode', 'run_id', 'duration'}
        need(set(scope) == fields)
        health = self.health()
        with self.journal.locked() as state:
            expire(state)
            if action == 'describe':
                need(scope['site_id'] == scope['domain_id'] == scope['mode'] == scope['run_id'] == '' and scope['duration'] == 0)
                need(state.get('active') is None)
                return {**scope, 'dedicated': True, 'modes': list(MODES)}
            need(identifier(scope['run_id']) and identifier(scope['site_id']) and scope['mode'] in MODES and
                 type(scope['duration']) is int and 480 <= scope['duration'] <= 4320)
            if scope['mode'] != 'analytics_query':
                need(identifier(scope['domain_id']))
                claim = {'site_id': scope['site_id'], 'domain_id': scope['domain_id']}
                need(state.get('claim') in (None, claim))
                if scope['mode'] == 'create_response':
                    if action == 'enter':
                        state['claim'] = claim
                else:
                    dns = state.get('dns')
                    need(dns is not None and (action != 'enter' or not dns.get('cleared')) and dns['site_id'] == scope['site_id'] and dns['domain_id'] == scope['domain_id'])
            elif scope['domain_id']:
                need(identifier(scope['domain_id']))
            run = state['faults'].get(scope['run_id'])
            if action == 'enter':
                need(run is None and state.get('active') is None, 'Overlapping or replayed provider fault refused')
                run = {'scope': scope, 'restore_at': time.time() + scope['duration'] + 90,
                       'failed_requests': 0, 'successful_requests_after_restore': 0, 'dropped_responses': 0,
                       'create_requests': 0, 'restored_at': None}
                state['faults'][scope['run_id']] = run
                state['active'] = scope['run_id']
            elif run is None and action == 'leave':
                # An enter may never have reached the controller. Do not clear
                # any other scope's fault in that case.
                return {**scope, 'fault_active': False}
            need(run is not None and run['scope'] == scope, 'Provider fault journal differs from the requested run')
            if action == 'leave':
                need(state.get('active') in (None, scope['run_id']), 'Another fault owns this relay')
                state['active'] = None
                run['restored_at'] = run['restored_at'] or time.time()
            need(action in ('enter', 'leave', 'status', 'inspect'))
            active = state.get('active') == scope['run_id']
            reply = {**scope, 'fault_active': active, 'automatic_restore_at': run['restore_at'],
                     **{k: run[k] for k in ('failed_requests', 'successful_requests_after_restore', 'dropped_responses', 'create_requests')}}
            if scope['mode'] == 'create_response' and action in ('status', 'inspect'):
                from urllib.parse import urlencode
                result = self.http.cloudflare(self.journal.config['hosting_read_token_file'], 'GET',
                    '/zones/' + self.journal.config['hosting_zone_id'] + '/custom_hostnames?' +
                    urlencode({'hostname': scope['hostname'], 'per_page': 50}))
                need(isinstance(result['result'], list) and len(result['result']) < 50 and result.get('result_info', {}).get('total_pages', 1) <= 1)
                reply['provider_objects'] = len(result['result'])
            else:
                reply['provider_objects'] = len(state.get('hosts', []))
        if action == 'status':
            status, _, _ = self.http.request('GET', scope['api_origin'] + '/healthz', timeout=3)
            reply['api_running'] = status == 200
            reply['fault_active'] = reply['fault_active'] and health.get('active') == scope['run_id']
        return reply


class Relay:
    def __init__(self, journal, http=None):
        self.journal, self.http = journal, http or HTTPS()

    def classify(self, method, target, body, state):
        cfg = self.journal.config
        url = urlsplit(target)
        need(not url.scheme and not url.netloc and not url.fragment and '%' not in url.path and '..' not in url.path)
        zone = '/client/v4/zones/' + cfg['hosting_zone_id']
        apps = '/client/v4/accounts/' + cfg['cf_account_id'] + '/access/apps'
        analytics = '/client/v4/accounts/' + cfg['cf_account_id'] + '/analytics_engine/sql'
        query = parse_qs(url.query, keep_blank_values=True)
        data = json.loads(body) if body and target != analytics else None
        need(method in ('GET', 'POST', 'PATCH', 'DELETE'))
        if url.path == analytics:
            need(method == 'POST' and not query and len(body) <= 16384)
            sql = body.decode()
            pattern = (r"SELECT toStartOfHour\(timestamp\) AS bucket, blob3 AS code, blob4 AS cache, double3 AS status,\n"
                r" SUM\(_sample_interval \* double1\) AS requests, SUM\(_sample_interval \* double2\) AS payload_bytes,\n"
                r" MAX\(_sample_interval\) AS sample_interval FROM " + re.escape(cfg['analytics_dataset']) +
                r"\n WHERE index1 = '([0-9a-f-]{36})' AND blob1 = '" + cfg['origin_id'] + r"' AND blob5 = 'v1'"
                r"\n AND timestamp >= toDateTime\('([0-9 :\-]{19})'\) AND timestamp < toDateTime\('([0-9 :\-]{19})'\)"
                r"\n GROUP BY bucket, code, cache, status ORDER BY bucket DESC LIMIT 2048 FORMAT JSON")
            match = re.fullmatch(pattern, sql)
            need(match is not None, 'Pilot relay accepts only the fixed bounded hosting analytics query')
            site = [match[1]]
            start, end = (datetime.strptime(match[n], '%Y-%m-%d %H:%M:%S') for n in (2, 3))
            need(identifier(site[0]) and end - start == timedelta(hours=24) and end.second == 0)
            need(state.get('site_id') in (None, site[0]), 'Analytics query escaped the pinned pilot site')
            state['site_id'] = site[0]
            return 'analytics_query', site[0], None
        if url.path == zone or url.path == zone + '/custom_hostnames/fallback_origin':
            need(method == 'GET' and not body and not query)
            return 'domain_api', None, None
        base = zone + '/custom_hostnames'
        if url.path == base:
            if method == 'POST':
                need(isinstance(data, dict) and set(data) == {'hostname', 'ssl'} and data.get('hostname') == cfg['hostname'] and
                     data.get('ssl') == {'method': 'txt', 'type': 'dv'})
                return 'create_response', None, data
            need(method == 'GET' and query.get('hostname') == [cfg['hostname']] and
                 set(query) <= {'hostname', 'per_page', 'page'})
            return 'domain_api', None, None
        if url.path.startswith(base + '/'):
            resource = url.path[len(base) + 1:]
            need(identifier(resource) and (method == 'GET' or resource in state.get('hosts', [])))
            return 'domain_api', None, data
        if url.path == apps:
            if method == 'POST':
                dns = state.get('dns')
                need(dns is not None and isinstance(data, dict) and data.get('domain') == cfg['hostname'] and
                     data.get('name') == 'gregale-internal-hosting-' + dns['domain_id'] + '-' + self.journal.provider_origin()[:12] and
                     data.get('type') == 'self_hosted' and data.get('policies') == [{'id': cfg['access_policy_id'], 'precedence': 1}] and
                     data.get('options_preflight_bypass') is False)
            else:
                need(method == 'GET' and query.get('domain') == [cfg['hostname']] and
                     set(query) <= {'name', 'domain', 'per_page', 'page'})
            return 'domain_api', None, data
        if url.path.startswith(apps + '/'):
            resource = url.path[len(apps) + 1:]
            need(identifier(resource) and (method == 'GET' or resource in state.get('apps', [])))
            return 'domain_api', None, data
        need(False, 'Pilot relay rejects unrelated provider endpoints')

    def request(self, method, target, headers, body):
        with self.journal.locked() as state:
            expire(state)
            kind, site, data = self.classify(method, target, body, state)
            need(isinstance(headers.get('authorization'), str) and headers['authorization'].startswith('Bearer '))
            active = state['faults'].get(state.get('active'))
            if active:
                scope = active['scope']
                need(site is None or site == scope['site_id'])
                if kind == 'create_response':
                    need(state.get('dns', {}).get('domain_id') == scope['domain_id'])
            if kind == 'create_response':
                for run in state['faults'].values():
                    if run['scope']['mode'] == 'create_response':
                        run['create_requests'] += 1
                atomic_json(self.journal.root / 'journal.json', state)
            fault_mode = active['scope']['mode'] if active else ''
            if fault_mode == kind and kind in ('domain_api', 'analytics_query'):
                active['failed_requests'] += 1
                return 503, b'{"success":false,"errors":[{"code":1000,"message":"isolated pilot fault"}]}'
            status, _, response = self.http.request(method, 'https://api.cloudflare.com' + target,
                {'Authorization': headers['authorization'], 'Content-Type': headers.get('content-type', 'application/json')}, body or None, timeout=20)
            success = 200 <= status < 300
            if kind != 'analytics_query' and success:
                envelope = json.loads(response)
                success = envelope.get('success') is True
                result = envelope.get('result')
                if success and isinstance(result, dict) and 'hostname' in result:
                    need(result['hostname'] == self.journal.config['hostname'], 'Provider hostname differs from the pilot')
                    if identifier(result.get('id')):
                        if method == 'DELETE':
                            state['hosts'] = [i for i in state.get('hosts', []) if i != result['id']]
                        elif result['id'] not in state.setdefault('hosts', []):
                            state['hosts'].append(result['id'])
                if success and isinstance(result, dict) and 'domain' in result:
                    need(result['domain'] == self.journal.config['hostname'] and result.get('type') == 'self_hosted')
                    if identifier(result.get('id')) and result['id'] not in state.setdefault('apps', []):
                        state['apps'].append(result['id'])
            expire(state)
            if success and kind == 'create_response' and active and state.get('active') and fault_mode == 'create_response':
                active['dropped_responses'] += 1
                active['failed_requests'] += 1
                return 503, b'{"success":false}'
            if success:
                for run in state['faults'].values():
                    if run['restored_at'] and (run['scope']['mode'] == kind or run['scope']['mode'] == 'domain_api' and kind == 'create_response'):
                        run['successful_requests_after_restore'] += 1
            return status, response


def serve(journal, http=None, ready=None, stopping=None):
    fd = os.open(journal.root / 'relay.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(fd)
        need(stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid() and info.st_mode & 0o077 == 0 and info.st_nlink == 1)
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise PilotError('A relay already owns this pilot directory') from None
        return _serve(journal, http, ready, stopping)
    finally:
        os.close(fd)


def _serve(journal, http=None, ready=None, stopping=None):
    relay = Relay(journal, http)
    socket = journal.root / 'relay.sock'
    need(not socket.exists() and not socket.is_symlink(), 'Pilot socket already exists; inspect the prior relay')
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass  # No request headers, URLs, credentials or bodies in logs.

        def dispatch(self):
            try:
                if self.path == '/__pilot_health' and self.command == 'GET':
                    with journal.locked() as state:
                        expire(state)
                        body = json.dumps({'ready': True, 'pid': os.getpid(), 'active': state.get('active')}).encode()
                    status = 200
                else:
                    need(self.headers.get('Transfer-Encoding') is None)
                    length = int(self.headers.get('Content-Length', 0))
                    need(0 <= length <= 1 << 20)
                    body = self.rfile.read(length)
                    need(len(body) == length)
                    status, body = relay.request(self.command, self.path, {k.lower(): v for k, v in self.headers.items()}, body)
            except (PilotError, OSError, ValueError, KeyError, TypeError):
                status, body = 502, b'{"success":false}'
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.send_header('Cache-Control', 'no-store')
            self.end_headers()
            try:
                self.wfile.write(body)
            except OSError:
                pass
        do_GET = do_POST = do_PATCH = do_DELETE = dispatch
    class Server(ThreadingMixIn, UnixStreamServer):
        daemon_threads = True
    stopping = stopping or threading.Event()
    # The watcher is in the long-running relay, independently of qualification
    # and short-lived controller processes. It never removes journals.
    def watchdog():
        while not stopping.wait(.25):
            with journal.locked() as state:
                expire(state)
    old_mask = os.umask(0o077)
    bound = False
    try:
        with Server(str(socket), Handler) as server:
            bound = True
            os.chmod(socket, 0o600)
            server.timeout = .25
            thread = threading.Thread(target=watchdog, daemon=True)
            thread.start()
            if ready:
                ready.set()
            try:
                while not stopping.is_set():
                    server.handle_request()
            finally:
                stopping.set()
                thread.join(timeout=2)
    finally:
        os.umask(old_mask)
        if bound:
            socket.unlink(missing_ok=True)
