#!/usr/bin/env python3
"""Exercise native MCP releases on explicitly named disposable Gregale apps."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import time

HERE = Path(__file__).resolve().parent
SCENARIOS = ('rollout', 'interrupt', 'bad-candidate', 'stale-observer')
BINDINGS = ('MCP_QUAL_RUNTIME_DATABASE_URL', 'MCP_QUAL_OBSERVER_DATABASE_URL',
            'MCP_QUAL_OPERATOR_DATABASE_URL', 'MCP_TASK_MIGRATION_DATABASE_URL',
            'MCP_TASK_OWNER_KEY', 'MCP_TASK_NAMESPACE')


class Blocked(Exception):
    pass


def write_json(path, value):
    with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, delete=False) as stream:
        temp = Path(stream.name)
        json.dump(value, stream, indent=2)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    try:
        os.replace(temp, path)
    finally:
        temp.unlink(missing_ok=True)


def prepare(starter, destination):
    if destination.exists():
        raise ValueError('Fixture destination must not exist')
    fixture = (HERE / 'fixtures/mcp-rollout-handler.js.txt').read_text()
    destination.mkdir(mode=0o700, parents=True)
    for name in ('web', 'web-bad', 'worker-old', 'worker-candidate', 'observer'):
        target = destination / name
        shutil.copytree(starter, target, ignore=shutil.ignore_patterns('node_modules', '.git', '.gregale', '*-fixture-*'))
        config_path = target / 'gregale-mcp.json'
        config = json.loads(config_path.read_text())
        config['tasks'].update(enabled=True, ttl_seconds=3600, poll_interval_ms=500,
                               worker_concurrency=3, shutdown_timeout_ms=1000,
                               max_attempts=10, retry_base_delay_ms=60000,
                               retry_max_delay_ms=60000, database_url_env='DATABASE_URL',
                               owner_key_env='MCP_TASK_OWNER_KEY', namespace_env='MCP_TASK_NAMESPACE')
        write_json(config_path, config)
        tasks_path = target / 'tasks.js'
        source = tasks_path.read_text()
        marker = 'export const mcpTaskHandlers = Object.freeze({'
        if source.count(marker) != 1:
            raise ValueError('Unsupported starter handler registry')
        tasks_path.write_text(source.split(marker)[0] + fixture.replace('__GENERATION__', 'old' if name == 'worker-old' else 'candidate'))
        if name.startswith('web'):
            server_path = target / 'server.js'
            server_path.write_text(server_path.read_text().replace("const role = process.env.MCP_TASKS_ROLE || 'combined';", "const role = 'web';"))
            if name == 'web-bad':
                app_path = target / 'app.js'
                source = app_path.read_text()
                needle = 'const app = express();'
                if source.count(needle) != 1:
                    raise ValueError('Unsupported starter Express app')
                app_path.write_text(source.replace(needle, needle + "\n  app.use((req, res, next) => req.path === config.endpoint ? res.status(503).json({ error: 'qualification rejection' }) : next());"))
        else:
            if name == 'observer':
                observer = target / 'tasks-observer.js'
                observer.write_text("import pg from 'pg';\nconst qualificationPool = new pg.Pool({ connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 5000 });\ntry { console.log(JSON.stringify({ event: 'mcp_qualification_observer_role', role: (await qualificationPool.query('SELECT current_user AS role')).rows[0].role })); } finally { await qualificationPool.end(); }\n" + observer.read_text())
            start = 'start:tasks-observer' if name == 'observer' else 'start:tasks-worker'
            (target / 'gregale.yaml').write_text(f'hosting:\n  start: npm run {start}\nworker:\n  drain_timeout: 45s\n  stop_signal: SIGTERM\n  scale:\n    min: 1\n    max: 1\n    metric: custom\n    name: mcp_tasks_outstanding\n    target: 4\n')
    write_json(destination / 'fixture.json', {'version': 1, 'purpose': 'disposable-mcp-qualification', 'scenarios': list(SCENARIOS)})
    return {'ok': True, 'fixture_directory': str(destination)}


def execute(command, env, timeout=60, cwd=None, allow_failure=False):
    try:
        result = subprocess.run(command, env=env, cwd=cwd, capture_output=True, text=True, timeout=timeout)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise RuntimeError('Qualification command unavailable or timed out') from error
    if result.returncode and not allow_failure:
        # Never include stdout/stderr: API errors, child output and database URLs can contain credentials.
        raise RuntimeError('Qualification command failed')
    return result


def json_command(command, env, timeout=60, cwd=None):
    result = execute(command, env, timeout, cwd)
    return json.loads(result.stdout)


def wait(check, timeout, description):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        result = check()
        if result:
            return result
        time.sleep(1)
    raise RuntimeError(description)


def stop_group(process):
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=10)


def qualify(args):
    report = {'version': 1, 'scope': 'native-mcp-hosting-rollout', 'reviewed_commit': args.commit,
              'scenario': args.scenario, 'status': 'blocked', 'native_observations': False, 'observations': {}}
    args.evidence.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if args.evidence.exists():
        raise ValueError('Evidence file must not exist; retain previous observations')
    process = None
    try:
        if not re.fullmatch(r'[0-9a-f]{40}', args.commit):
            raise Blocked('A full reviewed commit SHA is required')
        missing = [name for name in BINDINGS if not os.getenv(name)]
        if missing:
            raise Blocked('Missing environment bindings: ' + ', '.join(missing))
        if not os.environ['MCP_TASK_NAMESPACE'].startswith('mcp-qual-'):
            raise Blocked('Use a dedicated mcp-qual- namespace')
        plan = json.loads(args.plan.read_text())
        names = [plan[key] for key in ('web_app', 'worker_app', 'observer_app', 'observer_metric_app')] + plan['previous_worker_apps']
        if any(not re.fullmatch(r'mcp-qual-[a-z0-9-]+', name) for name in names):
            raise Blocked('Every target must have an mcp-qual- disposable app name')
        if len(plan['previous_worker_apps']) != 1:
            raise Blocked('Qualification requires one previous worker app')
        base = args.plan.resolve().parent
        worker = (base / plan['worker_path']).resolve()
        web = (base / plan['web_path']).resolve()
        if json.loads((args.fixtures / 'fixture.json').read_text()).get('purpose') != 'disposable-mcp-qualification':
            raise Blocked('Prepared qualification fixtures are required')
        if worker != (args.fixtures / 'worker-candidate').resolve() or web != (args.fixtures / ('web-bad' if args.scenario == 'bad-candidate' else 'web')).resolve():
            raise Blocked('Plan must use the matching prepared candidate fixtures')
        if args.state.exists() or Path(str(args.state) + '.gate').exists():
            raise Blocked('Use a fresh release journal for each qualification scenario')
        env = dict(os.environ, DATABASE_URL=os.environ['MCP_QUAL_RUNTIME_DATABASE_URL'])
        binary = str(args.binary.resolve())
        account = json_command([binary, '--json', 'whoami'], env)
        if account.get('id') != args.account:
            raise Blocked('Authenticated account differs from the dedicated qualification account')
        report['binary_sha256'] = hashlib.sha256(args.binary.read_bytes()).hexdigest()
        report['plan_sha256'] = hashlib.sha256(args.plan.read_bytes()).hexdigest()
        report['native_observations'] = True
        report['account_id'] = args.account
        def cli(*command, **options):
            return execute([binary, '--json', *command], env, **options)
        def probe(action, tasks=None):
            value = json_command(['node', str(HERE / 'mcp-rollout-probes.mjs'), str(worker), action],
                                 dict(env, MCP_QUAL_TASK_IDS=json.dumps(tasks or {})))
            if not value.get('ok'):
                raise RuntimeError('Database qualification probe rejected the run')
            return value
        execute(['node', 'tasks-migrate.js', 'apply'], env, cwd=worker)
        roles = probe('roles')['actors']
        report['observations']['database_roles'] = roles
        if args.bootstrap:
            for source, name in [(args.fixtures / 'worker-old', plan['previous_worker_apps'][0]), (args.fixtures / 'observer', plan['observer_app'])]:
                cli('deploy', '--path', str(source), '--source=worktree', '--name', name, '--app', '--wait', '--timeout', '600', timeout=660)
            cli('mcp', 'deploy', '--path', str(args.fixtures / 'web'), '--name', plan['web_app'], '--timeout', '600', timeout=660)
        def observer_role():
            result = cli('logs', plan['observer_app'], '--grep', 'mcp_qualification_observer_role')
            for line in result.stdout.splitlines():
                event = json.loads(line)
                payload = json.loads(event.get('line', '{}'))
                if payload.get('event') == 'mcp_qualification_observer_role':
                    return payload.get('role') == roles['observer']['role']
            return False
        if not observer_role():
            raise RuntimeError('Remote observer did not attest the observer database account')
        # App deployment history identifies the serving revision without running customer tools.
        def serving():
            result = cli('deployments', '--app', plan['web_app'], '--all')
            rows = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
            items = rows[0].get('items', rows) if len(rows) == 1 and isinstance(rows[0], dict) else rows
            selected = [item['id'] for item in items if item.get('status') == 'live' and item.get('traffic_percent') == 100]
            if len(selected) != 1:
                raise RuntimeError('Expected a single serving web revision')
            return selected[0]
        before = serving()
        tasks = probe('seed')['tasks']
        report['observations']['task_ids'] = tasks
        def initial_states():
            snapshots = probe('snapshot', tasks)['snapshots']
            if snapshots['running']['status'] == 'running' and snapshots['input-required']['status'] == 'input_required' and snapshots['retrying']['status'] == 'queued' and snapshots['retrying']['delayed'] and snapshots['retrying']['attempts'] > 0:
                return snapshots
            return None
        report['observations']['before_rollout'] = wait(initial_states, 120, 'Three durable Task states were not observed')
        if args.scenario == 'stale-observer':
            cli('park', plan['observer_app'])
            # No observer process may republish during this failure scenario.
            wait(lambda: not cli('ps', plan['observer_app']).stdout.strip(), 120, 'Observer did not park')
        command = [binary, '--json', 'mcp', 'tasks', 'release', '--plan', str(args.plan.resolve()), '--state', str(args.state.resolve())]
        process = subprocess.Popen(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
        known = None
        if args.scenario == 'interrupt':
            def queued():
                if args.state.exists():
                    state = json.loads(args.state.read_text())
                    if state.get('web_deployment') and state.get('stage') == 'deploying_web':
                        return state
                if process.poll() is not None:
                    raise RuntimeError('Release finished before the interruption window')
                return None
            known = wait(queued, 660, 'No durable web submission checkpoint before interruption')
            stop_group(process)
            process = subprocess.Popen(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
        stdout, _ = process.communicate(timeout=args.timeout)
        success = process.returncode == 0
        report['observations']['release_exit'] = process.returncode
        if args.scenario in ('bad-candidate', 'stale-observer'):
            failure = json.loads(stdout)
            if failure.get('stage') != 'start_replacements':
                raise RuntimeError('Failure did not occur at the candidate readiness gate')
            if success or serving() != before:
                raise RuntimeError('Failed health gate changed serving traffic')
            old_instances = cli('ps', plan['previous_worker_apps'][0])
            if not any(json.loads(line).get('state') == 'running' for line in old_instances.stdout.splitlines() if line.strip()):
                raise RuntimeError('Failed health gate did not preserve old workers')
            report['observations']['old_workers_preserved'] = True
        else:
            result = json.loads(stdout)
            if not success or not result.get('ok'):
                raise RuntimeError('Native release failed')
            state = json.loads(args.state.read_text())
            if known and any(state[key] != known[key] for key in ('web_deployment', 'worker_deployment')):
                raise RuntimeError('Resume created different candidate deployments')
            if serving() != state['web_deployment'] or state.get('stage') != 'complete':
                raise RuntimeError('Release completion and serving revision differ')
            report['observations']['deployment_ids'] = {key: state[key] for key in ('web_deployment', 'worker_deployment')}
            report['observations']['resumed_without_replacement'] = bool(known)
            probe('input', tasks)
            def completed():
                snapshots = probe('snapshot', tasks)['snapshots']
                if all(item['status'] == 'completed' and item['candidateResult'] and item['runtimeRole'] == roles['runtime']['role'] for item in snapshots.values()):
                    return snapshots
                if any(item['status'] in ('failed', 'cancelled') for item in snapshots.values()):
                    raise RuntimeError('A probe Task failed or was cancelled')
                return None
            report['observations']['completed'] = wait(completed, args.timeout, 'Probe Tasks did not complete on candidate runtime account')
        report['status'] = 'passed'
    except Blocked as error:
        report['detail'] = str(error)
    except Exception:
        report['status'] = 'failed'
        report['detail'] = 'Qualification failed; inspect deployment IDs and journals using the dedicated account. Child output is intentionally excluded.'
    finally:
        if process is not None:
            stop_group(process)
        write_json(args.evidence, report)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    prep = sub.add_parser('prepare')
    prep.add_argument('--starter', type=Path, required=True)
    prep.add_argument('--dir', type=Path, required=True)
    run = sub.add_parser('run')
    for name in ('plan', 'state', 'fixtures', 'binary', 'evidence'):
        run.add_argument('--' + name, type=Path, required=True)
    run.add_argument('--account', required=True)
    run.add_argument('--commit', required=True)
    run.add_argument('--scenario', choices=SCENARIOS, required=True)
    run.add_argument('--bootstrap', action='store_true')
    run.add_argument('--timeout', type=int, default=1200)
    args = parser.parse_args()
    if args.action == 'prepare':
        result = prepare(args.starter.resolve(), args.dir.resolve())
    else:
        result = qualify(args)
    print(json.dumps(result))
    return 0 if result.get('ok') or result.get('status') == 'passed' else 1


if __name__ == '__main__':
    raise SystemExit(main())
