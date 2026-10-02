"""Root-side RC231 gateway-only activation through pinned SSH; no database writes."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.request

BASE = 'f3c809386d718cd0276f89ace57e9a641ae5e9fa'
SOURCE = 'cb753d7cd9831a6b1c0547198faf48fa84b3beb7'
OLD_HASH = 'e2d2de6dfa244e0ad90eb43351ff77c52b757b8b81a1f66732ca735b535ade38'
UNIT = 'faas-gatewayd-internal.service'
OTHER_HASHES = {
    'vmmd': '71d3055e2e492e4e9254a01efa2127c848d2b2bcb0589a1ca71627a5e3381748',
    'schedd': '65965b99a44ff12e0bb2f49f1d42f2b32f462875da08cbb7caa083247c488cde',
    'imaged': '7199c65a0f90b0261ec0aa4caa315aa0223e381f973cfb6e557cc9d8bdcc9598',
    'builderd': '7c092ca68ad375e9e938d2ab6f2d0e2ee8f4821619ec4c3fe3ef999dcf8d735f',
    'realtimed': '361bba0a4b8afe6ff1ac40e24fc053da50eff3491e8ae9a049bca4d07d0fc3be',
}
ROOT = Path('/opt/faas/hotfixes/request-evidence-' + SOURCE)
DROP = Path('/etc/systemd/system/' + UNIT + '.d/98-audit-request-evidence-hotfix.conf')
BINARY = ROOT / 'gatewayd-internal'
CONTENT = ('# Audit-owned PR4085 hotfix; remove before a later full release.\n'
           '[Service]\nExecStart=\nExecStart=' + str(BINARY) +
           ' --config /etc/faas/gatewayd-internal.toml\n')


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def command(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=180)


def daemon(name):
    state = dict(x.split('=', 1) for x in command('systemctl', 'show', 'faas-' + name + '.service',
                 '--property=ActiveState,MainPID').splitlines())
    assert state['ActiveState'] == 'active' and int(state['MainPID']) > 0, state
    path = Path('/proc/' + state['MainPID'] + '/exe')
    return {'pid': int(state['MainPID']), 'exe': str(path.resolve()), 'sha256': digest(path)}


def snapshot():
    assert str(Path('/opt/faas/current').resolve()) == '/opt/faas/releases/' + BASE
    others = {n: daemon(n) for n in OTHER_HASHES}
    for n, row in others.items():
        assert row['sha256'] == OTHER_HASHES[n] and row['exe'] == '/opt/faas/releases/' + BASE + '/bin/' + n
    drops = command('systemctl', 'show', UNIT, '--property=DropInPaths', '--value').strip().split()
    return {'gateway': daemon('gatewayd-internal'), 'others': others,
            'other_dropins': {p: digest(p) for p in drops if p != str(DROP)}}


def health():
    for url in ['http://127.0.0.1:8080/healthz', 'http://127.0.0.1:9090/readyz']:
        req = urllib.request.Request(url, headers={'Host': 'gatewayd-internal.faas'})
        with urllib.request.urlopen(req, timeout=3) as response:
            assert response.status == 200
    req = urllib.request.Request('http://127.0.0.1:8080/v1/internal/metrics',
                                 headers={'Host': 'gatewayd-internal.faas'})
    with urllib.request.urlopen(req, timeout=3) as response:
        assert b'# HELP gateway_compute_node_changed_subscriber_alive ' in response.read()


def wait_health():
    end = time.monotonic() + 90
    while True:
        try:
            health()
            return
        except Exception:
            if time.monotonic() >= end:
                raise
            time.sleep(1)


def write_atomic(path, data, mode):
    temp = path.with_name(path.name + '.audit-tmp')
    assert not temp.exists(), str(temp)
    with temp.open('x') as f:
        os.chmod(temp, mode)
        f.write(data)
        f.flush()
        os.fsync(f.fileno())
    os.replace(temp, path)


def require_owned_dropin():
    assert not DROP.is_symlink() and DROP.read_text() == CONTENT, 'Unrelated override; refusing removal'


def rollback():
    if DROP.exists():
        require_owned_dropin()
        DROP.unlink()
        command('systemctl', 'daemon-reload')
        command('systemctl', 'restart', UNIT)
    wait_health()
    row = snapshot()
    assert row['gateway']['sha256'] == OLD_HASH
    assert row['gateway']['exe'] == '/opt/faas/releases/' + BASE + '/bin/gatewayd-internal'
    return {'status': 'rolled_back', 'snapshot': row}


def apply(config):
    assert digest(BINARY) == config['gateway_sha256'], 'Staged binary hash mismatch'
    before = snapshot()
    if DROP.exists():
        require_owned_dropin()
        assert before['gateway']['exe'] == str(BINARY) and before['gateway']['sha256'] == config['gateway_sha256']
        health()
        return {'status': 'already_applied', 'snapshot': before}
    assert before['gateway']['sha256'] == OLD_HASH and before['gateway']['exe'].endswith('/' + BASE + '/bin/gatewayd-internal')
    health()
    write_atomic(ROOT / 'before.json', json.dumps(before, indent=2) + '\n', 0o644)
    try:
        write_atomic(DROP, CONTENT, 0o644)
        command('systemctl', 'daemon-reload')
        command('systemctl', 'restart', UNIT)
        wait_health()
        after = snapshot()
        assert after['gateway']['exe'] == str(BINARY) and after['gateway']['sha256'] == config['gateway_sha256']
        assert after['others'] == before['others'], 'A non-gateway process changed'
        assert after['other_dropins'] == before['other_dropins'], 'An unrelated override changed'
        result = {'status': 'applied', 'source': SOURCE, 'base': BASE, 'before': before, 'after': after}
        write_atomic(ROOT / 'applied.json', json.dumps(result, indent=2) + '\n', 0o644)
        return result
    except BaseException:
        rollback()
        raise


def main(config):
    import fcntl
    import signal
    def cancelled(signum, frame):
        raise RuntimeError('Component hotfix interrupted')
    signal.signal(signal.SIGTERM, cancelled)
    signal.signal(signal.SIGINT, cancelled)
    assert config['source'] == SOURCE and config['base'] == BASE
    if config['action'] == 'inspect':
        state = snapshot()
        health()
        assert state['gateway']['sha256'] == OLD_HASH
        pid = str(state['gateway']['pid'])
        values = dict(x.split('=', 1) for x in Path('/proc/' + pid + '/environ').read_bytes().decode().split(chr(0)) if '=' in x)
        sql = """BEGIN READ ONLY; SET LOCAL statement_timeout='10s';
SELECT json_build_object('nodes',(SELECT json_agg(row_to_json(x)) FROM (
 SELECT id,name,lifecycle,vpcpus,vcpu_budget,mem_mb,admission_ceiling_mb,last_heartbeat_at FROM compute_nodes ORDER BY name) x),
 'live_instances',(SELECT json_agg(row_to_json(x)) FROM (
 SELECT i.id,a.slug,i.state,i.kind,i.mode,i.ram_mb,a.cpu_millicores,i.node_id,i.started_at,i.last_request_at
 FROM instances i LEFT JOIN apps a ON a.id=i.app_id
 WHERE i.state IN ('pending','waking','cold_booting','running','draining','snapshotting','migrating','warm')
 ORDER BY i.node_id,a.slug,i.started_at) x)); ROLLBACK;"""
        try:
            result = subprocess.run(['runuser', '-u', 'faas', '--', 'psql', '-X', '-q', '-A', '-t', '-v', 'ON_ERROR_STOP=1',
                                     '--dbname', values['DATABASE_URL']], input=sql, text=True,
                                    capture_output=True, timeout=25)
            capacity = json.loads(result.stdout) if result.returncode == 0 else {'query_exit': result.returncode}
        except Exception as error:
            capacity = {'error_type': type(error).__name__}
        return {'status': 'inspected', 'snapshot': state, 'readonly_capacity': capacity}
    with open('/run/faas/request-evidence-hotfix.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        action = config['action']
        if action == 'prepare':
            before = snapshot()
            if DROP.exists():
                require_owned_dropin()
                assert before['gateway']['exe'] == str(BINARY) and before['gateway']['sha256'] == config['gateway_sha256']
            else:
                assert before['gateway']['sha256'] == OLD_HASH
                assert before['gateway']['exe'] == '/opt/faas/releases/' + BASE + '/bin/gatewayd-internal'
            health()
            marker = ROOT / 'audit-owner.json'
            owner = {'source': SOURCE, 'base': BASE, 'component': 'gatewayd-internal', 'issue': 4083}
            if ROOT.exists():
                assert not ROOT.is_symlink() and marker.is_file() and not marker.is_symlink()
                assert json.loads(marker.read_text()) == owner, 'Unrelated directory; refusing staging'
            ROOT.mkdir(mode=0o755, parents=True, exist_ok=True)
            assert not ROOT.is_symlink()
            if not marker.exists():
                write_atomic(marker, json.dumps(owner, indent=2) + '\n', 0o644)
            return {'status': 'prepared', 'snapshot': before}
        if action == 'stage':
            incoming = ROOT / 'gatewayd-internal.incoming'
            assert not incoming.is_symlink() and digest(incoming) == config['gateway_sha256']
            if BINARY.exists():
                assert not BINARY.is_symlink() and digest(BINARY) == config['gateway_sha256']
                incoming.unlink()
            else:
                os.chmod(incoming, 0o755)
                os.replace(incoming, BINARY)
            return {'status': 'staged', 'sha256': digest(BINARY)}
        if action == 'apply':
            return apply(config)
        if action == 'rollback':
            return rollback()
        raise ValueError('Unknown action')
