#!/usr/bin/env python3
"""Run retained MCP regression probes without editing production packages."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

EVIDENCE = Path(__file__).resolve().parent
REPO = EVIDENCE.parents[3]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', help='Go 1.26.10 executable; otherwise use an available workspace toolchain or PATH')
    rounds = parser.add_mutually_exclusive_group()
    rounds.add_argument('--round2-only', action='store_true')
    rounds.add_argument('--round3-only', action='store_true')
    rounds.add_argument('--round4-only', action='store_true')
    rounds.add_argument('--round5-only', action='store_true')
    parser.add_argument('--node-starter', help='Temporary Node starter copy with dependencies installed')
    parser.add_argument('--output-dir', type=Path, default=EVIDENCE / 'remediation',
                        help='Log destination (defaults to remediation/ to preserve original evidence)')
    args = parser.parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    workspace_toolchains = sorted(Path('/workspace/faas-env/go/pkg/mod').glob('golang.org/toolchain@*go1.26.10*/bin/go'))
    go = args.go or (str(workspace_toolchains[-1]) if workspace_toolchains else shutil.which('go'))
    if not go:
        parser.error('supply --go with the Go 1.26.10 executable')
    env = os.environ.copy()
    if Path('/workspace/faas-env/go').is_dir():
        env.setdefault('GOPATH', '/workspace/faas-env/go')
        env.setdefault('GOCACHE', '/workspace/faas-env/cache/go-build')
    env.setdefault('GOMAXPROCS', '4')
    replacements = {
        str(REPO / 'pkg/mcpmanagement/zz_investigation_test.go'): str(EVIDENCE / 'management_repro_test.go.txt'),
        str(REPO / 'pkg/mcpmanagement/zz_investigation_round2_test.go'): str(EVIDENCE / 'management_round2_repro_test.go.txt'),
        str(REPO / 'pkg/mcphosting/zz_investigation_test.go'): str(EVIDENCE / 'hosting_repro_test.go.txt'),
        str(REPO / 'cmd/gregale/templates/mcp-go/zz_investigation_round3_test.go'): str(EVIDENCE / 'go_starter_round3_repro_test.go.txt'),
        str(REPO / 'pkg/mcphosting/zz_investigation_round4_test.go'): str(EVIDENCE / 'hosting_round4_repro_test.go.txt'),
        str(REPO / 'pkg/mcphosting/zz_investigation_round5_test.go'): str(EVIDENCE / 'hosting_round5_repro_test.go.txt'),
    }
    pattern = '^TestInvestigation(Cron|Inline|ReadRejects)' if args.round2_only else '^TestInvestigation(Durable|LongHTTP|ReviewedSource|UploadRecovery|Cron|Inline|ReadRejects)'
    summary = []
    with tempfile.TemporaryDirectory(prefix='gregale-mcp-probes-') as directory:
        overlay = Path(directory) / 'overlay.json'
        overlay.write_text(json.dumps({'Replace': replacements}))
        commands = [] if args.round3_only or args.round4_only or args.round5_only else [
            ('go_repros.log', [go, 'test', '-trimpath', '-gcflags=all=-dwarf=false', '-p=1',
                              '-overlay=' + str(overlay), './pkg/mcpmanagement', './pkg/mcphosting',
                              '-run', pattern, '-count=1', '-v', '-timeout=90s']),
            ('python_repros.log', [sys.executable, str(EVIDENCE / 'python_starter_repro_test.py'), '-v']),
        ]
        if not args.round2_only and not args.round4_only and not args.round5_only:
            commands.append(('python_round3_repros.log', [sys.executable, str(EVIDENCE / 'python_round3_repro_test.py'), '-v']))
            if args.node_starter:
                env['MCP_INVESTIGATION_NODE_STARTER'] = str(Path(args.node_starter).resolve())
                commands.append(('node_round3_repros.log', ['node', '--test', str(EVIDENCE / 'node_round3_repro_test.mjs')]))
            else:
                summary.append({'probe': 'node_round3', 'status': 'not_run', 'reason': 'supply --node-starter with dependencies installed'})
                print('Node round-three probes not run: supply --node-starter', flush=True)
            if env.get('MCP_TASKS_TEST_DATABASE_URL'):
                commands.append(('go_starter_round3_repros.log', [go, 'test', '-trimpath', '-gcflags=all=-dwarf=false', '-p=1',
                    '-overlay=' + str(overlay), './cmd/gregale/templates/mcp-go',
                    '-run', '^TestInvestigationBusyWorker', '-count=1', '-v', '-timeout=75s']))
            else:
                summary.append({'probe': 'go_starter_round3', 'status': 'not_run', 'reason': 'set MCP_TASKS_TEST_DATABASE_URL to a disposable PostgreSQL database'})
                print('Go heartbeat probe not run: set MCP_TASKS_TEST_DATABASE_URL (requires 47 seconds)', flush=True)
        if not args.round2_only and not args.round3_only and not args.round5_only:
            commands.append(('hosting_round4_repros.log', [go, 'test', '-trimpath', '-gcflags=all=-dwarf=false', '-p=1',
                '-overlay=' + str(overlay), './pkg/mcphosting', '-run',
                '^TestInvestigation(SchemaLiteralData|CatalogAcceptsStandardMetadata)', '-count=1', '-v', '-timeout=60s']))
            if args.node_starter:
                env['MCP_INVESTIGATION_NODE_STARTER'] = str(Path(args.node_starter).resolve())
                commands.append(('node_round4_repros.log', ['node', '--test', str(EVIDENCE / 'node_round4_repro_test.mjs')]))
                if not env.get('MCP_TASKS_TEST_DATABASE_URL'):
                    summary.append({'probe': 'node_round4_postgres', 'status': 'not_run', 'reason': 'set MCP_TASKS_TEST_DATABASE_URL to include the shutdown race probe'})
                    print('Node shutdown race probe not run: set MCP_TASKS_TEST_DATABASE_URL', flush=True)
            else:
                summary.append({'probe': 'node_round4', 'status': 'not_run', 'reason': 'supply --node-starter with dependencies installed'})
                print('Node round-four probes not run: supply --node-starter', flush=True)
        if not args.round2_only and not args.round3_only and not args.round4_only:
            commands.append(('hosting_round5_repros.log', [go, 'test', '-trimpath', '-gcflags=all=-dwarf=false', '-p=1',
                '-overlay=' + str(overlay), './pkg/mcphosting', '-run',
                '^TestInvestigation(SSEAcceptsResult|DoctorFailsRejected)', '-count=1', '-v', '-timeout=60s']))
            if args.node_starter and env.get('MCP_TASKS_TEST_DATABASE_URL'):
                env['MCP_INVESTIGATION_NODE_STARTER'] = str(Path(args.node_starter).resolve())
                commands.append(('node_round5_repros.log', ['node', '--test', str(EVIDENCE / 'node_round5_repro_test.mjs')]))
            else:
                summary.append({'probe': 'node_round5', 'status': 'not_run', 'reason': 'supply --node-starter and set MCP_TASKS_TEST_DATABASE_URL for the key-preflight probe'})
                print('Node key-preflight probe not run: supply --node-starter and set MCP_TASKS_TEST_DATABASE_URL', flush=True)
        for log_name, command in commands:
            result = subprocess.run(command, cwd=REPO, env=env, stdout=subprocess.PIPE,
                                    stderr=subprocess.STDOUT, text=True, timeout=240)
            (args.output_dir / log_name).write_text(result.stdout)
            summary.append({'log': log_name, 'exit_code': result.returncode})
            print(f'{log_name}: exit {result.returncode}', flush=True)
    selected_round = next((number for number in (3, 4, 5) if getattr(args, f'round{number}_only')), None)
    results_file = f'round{selected_round}_probe_results.json' if selected_round else 'probe_results.json'
    (args.output_dir / results_file).write_text(json.dumps(summary, indent=2) + '\n')
    return 1 if any(item.get('exit_code') or item.get('status') == 'not_run' for item in summary) else 0


if __name__ == '__main__':
    raise SystemExit(main())
