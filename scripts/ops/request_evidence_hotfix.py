"""Exact-source signed component hotfix under the production-us CD concurrency lock."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import shutil
import subprocess
import sys
import tarfile
import tempfile

import request_evidence_hotfix_host as host

REPO = 'poyrazK/faas'
TARGETS = [
    ('fsn-2.gregale.dev', 'SHA256:0p1vgiWlG75HcPGAGJFTCTf2GudZsX2h3VJFrnrkc6k'),
    ('fsn-3.gregale.dev', 'SHA256:TvYJYcE8Hb6FYq6fTY9bHG+QyzzAHn4j9Qqm5vBILck'),
]
RECEIPT = Path('request-evidence-hotfix-receipt.json')


def run(args, timeout=600, **kwargs):
    return subprocess.check_output(args, text=True, stderr=subprocess.PIPE, timeout=timeout, **kwargs)


def api(path):
    return json.loads(run(['gh', 'api', 'repos/' + REPO + '/' + path]))


def qualify():
    for rid in [36979712031, 36979661246]:
        value = api('actions/runs/' + str(rid))
        assert value['head_sha'] == host.SOURCE and value['conclusion'] == 'success'
        jobs = api('actions/runs/' + str(rid) + '/jobs?per_page=100')['jobs']
        assert len(jobs) == 18 and all(j['conclusion'] == 'success' for j in jobs)
        if rid == 36979712031:
            assert any(s['name'] == 'migrations per-migration tests (full package)' and s['conclusion'] == 'success'
                       for j in jobs for s in j['steps'])
    runs = api('actions/runs?head_sha=' + host.SOURCE + '&per_page=100')['workflow_runs']
    for name in ['codeql', 'images', 'MCP contract', 'Customer platform starter', 'cve-check']:
        assert any(r['name'] == name and r['conclusion'] == 'success' for r in runs), name
    with tempfile.TemporaryDirectory(prefix='hotfix-gates-') as tmp:
        root = Path(tmp)
        run(['gh', 'run', 'download', '36979712031', '--repo', REPO,
             '--name', 'operation-policy-postgres-contract', '--dir', str(root / 'pg')])
        validate_postgres((root / 'pg/operation-policy-postgres-contract.log').read_text())
        cve = api('actions/runs/36981938659')
        assert cve['head_sha'] == host.SOURCE and cve['conclusion'] == 'success'
        jobs = api('actions/runs/36981938659/jobs?per_page=100')['jobs']
        required = {'Generate SBOM', 'Refresh Grype vulnerability database', 'Run grype on SBOM',
                    'Run govulncheck', 'Normalize scanner results', 'Diff + create issue'}
        assert required <= {s['name'] for j in jobs for s in j['steps'] if s['conclusion'] == 'success'}
        run(['gh', 'run', 'download', '36981938659', '--repo', REPO,
             '--name', 'cve-prev', '--dir', str(root / 'cve')])
        for name in ['cve-prev.json', 'cve-today.json', 'cve-new.json']:
            matches = list((root / 'cve').rglob(name))
            assert len(matches) == 1 and json.loads(matches[0].read_text()) == []


def validate_postgres(log):
    for test in ['TestExclusivePolicyRetirement', 'TestExclusivePolicyRetirement/postgres']:
        assert '=== RUN   ' + test + '\n' in log
        assert re.search(r'^--- PASS: ' + re.escape(test) + r' \(', log, re.M)
    assert not re.search(r'--- (SKIP|FAIL):', log)
    assert 'operation-policy-postgres-check: PostgreSQL retirement' in log


def extract_gateway(archive, manifest_bytes, destination):
    m = json.loads(manifest_bytes)
    assert m['git_sha'] == host.SOURCE
    expected = m['daemon_hashes']['gatewayd_internal'].removeprefix('sha256:')
    assert re.fullmatch('[0-9a-f]{64}', expected)
    with tarfile.open(archive, 'r:gz') as tb:
        manifests = [x for x in tb.getmembers() if Path(x.name).name == 'release-manifest.json' and x.isfile()]
        gateways = [x for x in tb.getmembers() if x.name == 'gatewayd-internal' and x.isfile()]
        assert len(manifests) == len(gateways) == 1
        assert tb.extractfile(manifests[0]).read() == manifest_bytes
        assert 0 < gateways[0].size < 150_000_000
        data = tb.extractfile(gateways[0]).read()
    assert hashlib.sha256(data).hexdigest() == expected
    destination.write_bytes(data)
    return expected


def public_health():
    status = run(['curl', '--fail', '--silent', '--show-error', '--max-time', '15',
                  '--output', '/dev/null', '--write-out', '%{http_code}',
                  'https://api.gregale.dev/healthz'])
    assert status == '200'
    results = {'api': 200}
    for name, url, payload in [
        ('app', 'https://audit-1001-deep.gregale.dev/info', None),
        ('function', 'https://audit-1001-py-current.gregale.dev/', {'marker': 'rc232-gateway-hotfix'}),
    ]:
        args = ['curl', '--fail', '--silent', '--show-error', '--max-time', '45',
                '--write-out', '\n%{http_code}']
        if payload is not None:
            args += ['--header', 'Content-Type: application/json', '--data', json.dumps(payload)]
        body, status = run(args + [url]).rsplit('\n', 1)
        value = json.loads(body)
        assert status == '200' and value.get('ok') is True
        assert value.get('boot') if name == 'app' else value.get('invocation_id')
        results[name] = {'status': 200, 'response': value}
    return results


def deploy(remote, upload, public_gate, record):
    attempted = []
    try:
        for target, _ in TARGETS:
            record(target, remote(target, 'prepare'))
            upload(target)
            record(target, remote(target, 'stage'))
            attempted.append(target)
            record(target, remote(target, 'apply'))
            record(target, {'status': 'public_ingress_passed', 'gates': public_gate()})
    except BaseException:
        failures = []
        for target in reversed(attempted):
            try:
                record(target, remote(target, 'rollback'))
            except BaseException as error:
                failures.append({'target': target, 'error_type': type(error).__name__})
        if failures:
            record('rollback_failures', failures)
        raise


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--cosign', required=True)
    parser.add_argument('--operation', choices=['inspect', 'apply'], default='inspect')
    args = parser.parse_args()
    def cancelled(signum, frame):
        raise RuntimeError('Component hotfix interrupted')
    signal.signal(signal.SIGTERM, cancelled)
    signal.signal(signal.SIGINT, cancelled)
    tag = os.environ['HOTFIX_TAG']
    assert re.fullmatch(r'v0\.1\.18-rc\.[0-9]+', tag)
    assert os.environ.get('COMPUTE_SSH_KEY')
    receipt = {'source': host.SOURCE, 'base': host.BASE, 'tag': tag, 'events': [], 'status': 'preflight'}
    def record(target, value):
        receipt['events'].append({'target': target, 'result': value})
        RECEIPT.write_text(json.dumps(receipt, indent=2) + '\n')
    record('intent', {'component': 'gatewayd-internal', 'control_plane_changed': False,
                      'shared_current_changed': False, 'database_changed': False})
    try:
        qualify()
        ref = api('git/ref/tags/' + tag)['object']
        assert ref['type'] == 'commit' and ref['sha'] == host.SOURCE
        with tempfile.TemporaryDirectory(prefix='request-evidence-', dir=os.environ['RUNNER_TEMP']) as tmp:
            root = Path(tmp)
            run(['gh', 'release', 'download', tag, '--repo', REPO, '--dir', str(root),
                 '--pattern', 'release.tar.gz', '--pattern', 'release.cosign.bundle',
                 '--pattern', 'release-manifest.json', '--pattern', 'SHA256SUMS',
                 '--pattern', 'production-manifest.yaml', '--pattern', 'runtime-bases.env'], timeout=1800)
            sums = dict((line.split(maxsplit=1)[1].lstrip('*'), line.split(maxsplit=1)[0])
                        for line in (root / 'SHA256SUMS').read_text().splitlines())
            assert host.digest(root / 'release.tar.gz') == sums['release.tar.gz']
            run([args.cosign, 'verify-blob', '--bundle', str(root / 'release.cosign.bundle'),
                 '--certificate-identity', 'https://github.com/' + REPO + '/.github/workflows/release.yml@refs/tags/' + tag,
                 '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com', str(root / 'release.tar.gz')])
            gateway_hash = extract_gateway(root / 'release.tar.gz', (root / 'release-manifest.json').read_bytes(), root / 'gatewayd-internal')
            record('signed_bundle', {'sha256': sums['release.tar.gz'], 'gateway_sha256': gateway_hash, 'signature_verified': True})
            run([sys.executable, str(Path(__file__).with_name('request_evidence_hotfix_images.py')), str(root), tag])
            label = 'rc' + tag.rsplit('.', 1)[-1]
            image_receipt = root / (label + '-image-manifest-verification.json')
            record('image_manifests', json.loads(image_receipt.read_text()))
            image_evidence = Path('request-evidence-image-evidence')
            image_evidence.mkdir(exist_ok=True)
            for path in root.glob(label + '-image-*.json'):
                shutil.copy2(path, image_evidence / path.name)
            key = root / 'ssh-key'; key.write_text(os.environ['COMPUTE_SSH_KEY']); key.chmod(0o600)
            known = root / 'known-hosts'
            for target, expected in TARGETS:
                scan = run(['ssh-keyscan', '-T', '10', '-t', 'ed25519', target])
                part = root / (target + '.known'); part.write_text(scan)
                fingerprints = {line.split()[1] for line in run(['ssh-keygen', '-lf', str(part), '-E', 'sha256']).splitlines()}
                assert fingerprints == {expected}
                with known.open('a') as f: f.write(scan)
            options = ['-i', str(key), '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes',
                       '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(known), '-o', 'ConnectTimeout=15']
            code = Path(host.__file__).read_text()
            def remote(target, action):
                cfg = {'source': host.SOURCE, 'base': host.BASE, 'gateway_sha256': gateway_hash, 'action': action}
                script = code + '\nprint(json.dumps(main(' + repr(cfg) + ')))\n'
                return json.loads(run(['ssh', *options, 'root@' + target, 'python3 -'], input=script))
            def upload(target):
                run(['scp', *options, str(root / 'gatewayd-internal'), 'root@' + target + ':' + str(host.ROOT / 'gatewayd-internal.incoming')])
            if args.operation == 'inspect':
                for target, _ in TARGETS:
                    record(target, remote(target, 'inspect'))
                record('public_ingress', {'status': 'public_ingress_passed', 'gates': public_health()})
            else:
                record('before_activation', {'status': 'public_ingress_passed', 'gates': public_health()})
                deploy(remote, upload, public_health, record)
        receipt['status'] = 'inspected' if args.operation == 'inspect' else 'applied'
        record('complete', {'operation': args.operation, 'activation_performed': args.operation == 'apply',
                            'live_scenario_acceptance_pending': True})
    except BaseException as error:
        receipt['status'] = 'failed'
        record('failure', {'error_type': type(error).__name__})
        raise
    print(json.dumps(receipt, indent=2))


if __name__ == '__main__':
    main()
