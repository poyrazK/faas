"""Private pilot configuration, durable local journals, and bounded HTTPS."""
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import tempfile
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


class PilotError(Exception):
    pass


def need(condition, message='Pilot adapter rejected unsafe or inconsistent state'):
    if not condition:
        raise PilotError(message)


def identifier(value):
    return isinstance(value, str) and re.fullmatch(r'[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}', value) is not None


def hex_id(value, length=32):
    return isinstance(value, str) and re.fullmatch('[0-9a-f]{' + str(length) + '}', value) is not None


def hostname(value):
    return isinstance(value, str) and len(value) <= 253 and re.fullmatch(r'(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}', value) is not None


def private_directory(path):
    path = Path(path)
    need(path.is_absolute() and path.resolve() == path)
    info = path.lstat()
    need(stat.S_ISDIR(info.st_mode) and info.st_uid == os.geteuid() and info.st_mode & 0o077 == 0,
         'Pilot directory must be private and owned by this uid')
    return path


def private_read(path, maximum=65536):
    path = Path(path)
    need(path.is_absolute())
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        need(stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid() and info.st_mode & 0o077 == 0 and info.st_nlink == 1,
             'Pilot files must be private regular files owned by this uid')
        value = stream.read(maximum + 1)
    need(len(value) <= maximum, 'Pilot file exceeded its bound')
    return value


def atomic_json(path, value):
    with tempfile.NamedTemporaryFile(dir=path.parent, mode='w', delete=False) as stream:
        tmp = Path(stream.name)
        try:
            json.dump(value, stream, sort_keys=True)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
            os.replace(tmp, path)
            fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
        finally:
            tmp.unlink(missing_ok=True)


def configuration(value):
    need(isinstance(value, dict) and value.get('version') == 1)
    required = {'version', 'api_origin', 'account_id', 'origin_id', 'worker_name', 'hostname', 'hosting_domain',
                'cf_account_id', 'hosting_zone_id', 'access_policy_id', 'dns_account_id', 'dns_zone_id', 'dns_zone_name',
                'access_team', 'analytics_dataset', 'dns_token_file', 'hosting_read_token_file', 'domain_access_token_file'}
    need(set(value) == required, 'Pilot configuration has missing or unknown fields')
    url = urlsplit(value['api_origin'])
    need(url.scheme == 'https' and url.hostname and not url.username and not url.password and
         not url.query and not url.fragment and url.path == '' and not url.port)
    need(identifier(value['account_id']) and identifier(value['access_policy_id']) and hex_id(value['origin_id'], 64))
    need(all(hex_id(value[k]) for k in ('cf_account_id', 'hosting_zone_id', 'dns_account_id', 'dns_zone_id')))
    need(value['dns_account_id'] != value['cf_account_id'] and value['dns_zone_id'] != value['hosting_zone_id'],
         'Customer DNS must use a separate Cloudflare account and zone')
    need(all(hostname(value[k]) for k in ('hostname', 'hosting_domain', 'dns_zone_name', 'access_team')))
    need(len(value['hostname']) <= 64 and value['hostname'].endswith('.' + value['dns_zone_name']) and
         not value['hostname'].endswith('.' + value['hosting_domain']) and value['hostname'] != value['hosting_domain'] and
         not value['hosting_domain'].endswith('.' + value['hostname']) and
         url.hostname != value['hostname'] and not url.hostname.endswith('.' + value['hostname']) and
         not value['hostname'].endswith('.' + url.hostname))
    need(value['access_team'].endswith('.cloudflareaccess.com'))
    need(isinstance(value['worker_name'], str) and re.fullmatch(r'gregale-internal-[a-z0-9-]{1,45}', value['worker_name']) is not None)
    need(isinstance(value['analytics_dataset'], str) and re.fullmatch(r'gregale_internal_[a-z0-9_]{1,47}', value['analytics_dataset']) is not None)
    for key in ('dns_token_file', 'hosting_read_token_file', 'domain_access_token_file'):
        need(isinstance(value[key], str) and Path(value[key]).is_absolute() and '\n' not in value[key])
    return value


def config_digest(cfg):
    return hashlib.sha256(json.dumps(cfg, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


class Journal:
    def __init__(self, root):
        self.root = private_directory(root)
        self.config = configuration(json.loads(private_read(self.root / 'config.json')))
        state = json.loads(private_read(self.root / 'journal.json', 1 << 20))
        need(state.get('config_sha256') == config_digest(self.config), 'Prepared pilot configuration changed; preserve the original journal')

    @contextmanager
    def locked(self):
        fd = os.open(self.root / 'lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        try:
            info = os.fstat(fd)
            need(stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid() and info.st_mode & 0o077 == 0 and info.st_nlink == 1)
            fcntl.flock(fd, fcntl.LOCK_EX)
            path = self.root / 'journal.json'
            state = json.loads(private_read(path, 1 << 20))
            need(state.get('config_sha256') == config_digest(self.config))
            before = json.dumps(state, sort_keys=True)
            yield state
            if json.dumps(state, sort_keys=True) != before:
                atomic_json(path, state)
        finally:
            os.close(fd)

    def identity(self, request):
        keys = ('version', 'api_origin', 'account_id', 'origin_id', 'worker_name', 'hostname')
        need(isinstance(request, dict) and all(type(request.get(k)) is type(self.config[k]) and request[k] == self.config[k] for k in keys))
        return {k: request[k] for k in keys}

    def provider_origin(self):
        cfg = self.config
        return hashlib.sha256('\n'.join([cfg['cf_account_id'], cfg['hosting_zone_id'], cfg['access_policy_id'],
                                        'customers.' + cfg['hosting_domain']]).encode()).hexdigest()


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class HTTPS:
    def request(self, method, url, headers=None, body=None, timeout=20):
        parsed = urlsplit(url)
        need(parsed.scheme == 'https' and parsed.hostname and not parsed.username and not parsed.fragment)
        try:
            response = build_opener(NoRedirect()).open(Request(url, method=method, headers=headers or {}, data=body), timeout=timeout)
        except HTTPError as error:
            response = error
        except (OSError, URLError, ValueError):
            raise PilotError('Pilot HTTPS request failed') from None
        with response:
            data = response.read((1 << 20) + 1)
            need(len(data) <= 1 << 20, 'Pilot HTTPS response exceeded its bound')
            return response.code, {k.lower(): v for k, v in response.headers.items()}, data

    def cloudflare(self, token_path, method, path, value=None):
        need(path.startswith('/zones/') or path.startswith('/accounts/'))
        token = private_read(token_path, 16384).decode().strip()
        need(token and '\n' not in token and '\r' not in token)
        body = None if value is None else json.dumps(value).encode()
        status, _, data = self.request(method, 'https://api.cloudflare.com/client/v4' + path,
                                       {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}, body)
        need(200 <= status < 300, 'Cloudflare pilot request failed; inspect provider state separately')
        reply = json.loads(data)
        need(reply.get('success') is True and 'result' in reply, 'Cloudflare pilot request was not acknowledged')
        return reply
