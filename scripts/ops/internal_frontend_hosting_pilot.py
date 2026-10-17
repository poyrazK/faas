#!/usr/bin/env python3
"""Prepare, run and recover an explicitly isolated hosting pilot. Never provisions infrastructure."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import threading

from internal_frontend_hosting_pilot_common import Journal, PilotError, atomic_json, config_digest, configuration, need, private_read
from internal_frontend_hosting_pilot_dns import DNSController
from internal_frontend_hosting_pilot_relay import ProviderController, local_health, serve

HERE = Path(__file__).resolve().parent
RUNNER = HERE / 'internal-frontend-hosting-qualification.py'


def prepare(config, directory):
    cfg = configuration(json.loads(private_read(config)))
    need(directory.is_absolute() and not directory.exists() and not directory.is_symlink())
    need(len(str(directory / 'relay.sock')) <= 100, 'Pilot relay socket path must be at most 100 bytes')
    directory.mkdir(mode=0o700)
    need(directory.resolve() == directory)
    atomic_json(directory / 'config.json', cfg)
    atomic_json(directory / 'journal.json', {'version': 1, 'config_sha256': config_digest(cfg), 'faults': {}, 'active': None, 'hosts': [], 'apps': []})
    for kind in ('domain', 'provider'):
        code = '#!/usr/bin/env python3\nimport sys\nsys.path.insert(0, ' + repr(str(HERE)) + ')\n'
        code += 'from internal_frontend_hosting_pilot import controller_main\n'
        code += 'raise SystemExit(controller_main(' + repr(str(directory)) + ', ' + repr(kind) + '))\n'
        path = directory / (kind + '-controller')
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o700)
        with os.fdopen(fd, 'w') as stream:
            stream.write(code)
    return {'directory': str(directory), 'relay_socket': str(directory / 'relay.sock'),
            'apid_environment': {'FAAS_FRONTEND_HOSTING_PILOT_ENABLED': 'true',
                                 'FAAS_FRONTEND_HOSTING_PILOT_RELAY_SOCKET': str(directory / 'relay.sock')}}


def controller_main(root, kind):
    try:
        need(len(sys.argv) == 2)
        data = sys.stdin.buffer.read(65537)
        need(len(data) <= 65536)
        request = json.loads(data)
        journal = Journal(root)
        controller = DNSController(journal) if kind == 'domain' else ProviderController(journal)
        result = controller.run(sys.argv[1], request)
        encoded = json.dumps(result)
        need(len(encoded.encode()) <= 16384)
        print(encoded)
        return 0
    except (PilotError, OSError, ValueError, KeyError, TypeError, AttributeError):
        print('Pilot controller failed; inspect its protected journal and configured provider separately', file=sys.stderr)
        return 1


def runner_args(journal, args):
    cfg = journal.config
    return ['--infrastructure', str(args.infrastructure), '--binary', args.binary, '--terraform', args.terraform,
            '--aws', args.aws, '--account', cfg['account_id'], '--project', args.project, '--environment', args.environment,
            '--custom-domain', cfg['hostname'], '--domain-controller', str(journal.root / 'domain-controller'),
            '--provider-controller', str(journal.root / 'provider-controller'), '--evidence', str(args.evidence),
            '--edge-timeout', str(args.edge_timeout), '--cleanup-timeout', str(args.cleanup_timeout)]


def qualification(journal, args):
    need(os.environ.get('FAAS_API', '').rstrip('/') == journal.config['api_origin'], 'FAAS_API differs from the prepared pilot')
    local_health(journal.root)
    module = load_runner()
    deployed = module.Infrastructure(args.terraform, args.infrastructure, os.environ).output()
    need(deployed.get('account_id') == journal.config['cf_account_id'] and
         all(deployed.get(k) == journal.config[k] for k in ('origin_id', 'worker_name', 'hosting_domain', 'analytics_dataset')),
         'Prepared pilot identity differs from the applied Terraform output')
    command = [sys.executable, str(RUNNER), *runner_args(journal, args)]
    if args.check_usage:
        command.append('--check-usage')
    if args.check_traffic or args.provider_failures:
        command.append('--check-traffic')
    if args.provider_failures:
        need(args.domain_recovery_review is not None, 'Provider-failure qualification requires a new operator review path')
        command += ['--check-domain-recovery', '--domain-recovery-review', str(args.domain_recovery_review),
                    '--check-domain-provider-outage', '--check-analytics-outage']
    # Nothing enables admission, deploys a Worker, writes Terraform, or changes
    # apid settings. The already provisioned dedicated pilot is a prerequisite.
    return subprocess.run(command, check=False).returncode


def load_runner():
    spec = importlib.util.spec_from_file_location('hosting_qualification_cleanup', RUNNER)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def recovery_cleanup(journal, args):
    """Resume the existing deletion/storage/tombstone checks, never erase journals."""
    need(os.environ.get('FAAS_API', '').rstrip('/') == journal.config['api_origin'])
    evidence = json.loads(private_read(args.evidence, 1 << 20))
    cfg = journal.config
    need(evidence.get('scope') == 'internal-static-frontend-hosting' and evidence.get('account_id') == cfg['account_id'] and
         evidence.get('project') == args.project and evidence.get('environment') == args.environment)
    deployed = evidence.get('deployment', {})
    need(all(deployed.get(k) == cfg[k] for k in ('origin_id', 'worker_name')) and deployed.get('hosting_domain') == cfg['hosting_domain'])
    from internal_frontend_hosting_pilot_common import identifier
    sid = evidence.get('site_id')
    domain = evidence.get('custom_domain', {})
    need(identifier(sid) and domain.get('hostname') == cfg['hostname'] and identifier(domain.get('run_id')))
    module = load_runner()
    options = argparse.Namespace(terraform=args.terraform, infrastructure=args.infrastructure, binary=args.binary, aws=args.aws,
        account=cfg['account_id'], project=args.project, environment=args.environment, evidence=args.evidence,
        edge_timeout=args.edge_timeout, cleanup_timeout=args.cleanup_timeout, check_worker_switch=False,
        custom_domain=cfg['hostname'], domain_controller=journal.root / 'domain-controller', provider_controller=journal.root / 'provider-controller')
    q = module.Qualification(options)
    q.api_url = cfg['api_origin']
    q.deployment = q.infrastructure.output()
    need(q.deployment.get('account_id') == cfg['cf_account_id'] and
         all(q.deployment.get(k) == deployed.get(k) for k in ('account_id', 'origin_id', 'bucket', 'hosting_domain', 'worker_name')))
    need(q.cli('whoami').get('id') == cfg['account_id'], 'Cleanup API token belongs to a different account')
    controller = ProviderController(journal)
    # Restore the actual journal, not an untrusted report-selected arbitrary run.
    with journal.locked() as state:
        scopes = [run['scope'] for run in state['faults'].values()]
        pinned = state.get('site_id') or state.get('claim', {}).get('site_id')
        need(pinned in (None, sid) and all(scope['site_id'] == sid for scope in scopes), 'Cleanup evidence differs from the pinned pilot site')
    for scope in scopes:
        reply = controller.run('leave', scope)
        need(reply.get('fault_active') is False, 'Confirm fault restoration before cleanup')
        for entry in [evidence.get('provider_fault', {}), *evidence.get('provider_faults', [])]:
            if entry.get('run_id') == scope['run_id']:
                entry['status'] = 'restored'
    q.report = evidence
    q.domain_run = domain['run_id']
    q.domain_dns_dirty = domain.get('dns_cleanup') == 'required'
    q.domain = {'id': domain['id']} if identifier(domain.get('id')) else None
    status, _, body = q.api_response('GET', '/' + sid)
    if status == 200:
        q.site = q.accept_site(module.decode_json(body), sid)
        need(q.site.get('durable_routing') is True and q.site.get('name') == evidence.get('site_name') and
             isinstance(evidence.get('site_name'), str) and evidence['site_name'].startswith('hosting-qual-'))
        q.cleanup()
    else:
        need(status == 404 and module.decode_json(body).get('code') == 'not_found')
        q.site = {'id': sid, 'hostname': sid + '.' + cfg['hosting_domain'], 'origin_id': cfg['origin_id'], 'durable_routing': True}
        q.report['cleanup'] = {'status': 'requested', 'site_id': sid}
        q.verify_removed_site(0)
    q.report['pilot_recovery_cleanup'] = 'passed'
    q.save()
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subs = parser.add_subparsers(dest='command', required=True)
    init = subs.add_parser('prepare', help='Create new private local files only; no provider calls')
    init.add_argument('--config', type=Path, required=True)
    init.add_argument('--directory', type=Path, required=True)
    relay = subs.add_parser('serve', help='Run the private relay/watchdog on the dedicated apid host')
    relay.add_argument('--directory', type=Path, required=True)
    for verb in ('qualify', 'cleanup'):
        run = subs.add_parser(verb)
        run.add_argument('--directory', type=Path, required=True)
        run.add_argument('--infrastructure', type=Path, required=True)
        run.add_argument('--binary', default='gregale')
        run.add_argument('--terraform', default='terraform')
        run.add_argument('--aws', default='aws')
        run.add_argument('--project', required=True)
        run.add_argument('--environment', required=True)
        run.add_argument('--evidence', type=Path, required=True)
        run.add_argument('--edge-timeout', type=int, default=900)
        run.add_argument('--cleanup-timeout', type=int, default=900)
        if verb == 'qualify':
            run.add_argument('--check-usage', action='store_true')
            run.add_argument('--check-traffic', action='store_true')
            run.add_argument('--provider-failures', action='store_true')
            run.add_argument('--domain-recovery-review', type=Path)
    args = parser.parse_args()
    try:
        if args.command == 'prepare':
            print(json.dumps(prepare(args.config, args.directory), indent=2))
            return 0
        journal = Journal(args.directory)
        if args.command == 'serve':
            stopping = threading.Event()
            signal.signal(signal.SIGTERM, lambda *_: stopping.set())
            signal.signal(signal.SIGINT, lambda *_: stopping.set())
            serve(journal, stopping=stopping)
            return 0
        if args.command == 'qualify':
            return qualification(journal, args)
        return recovery_cleanup(journal, args)
    except Exception:  # Tool failures may contain credentials; never print their raw exceptions.
        print('Pilot workflow failed; preserve its journal and evidence and inspect configured services separately', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
