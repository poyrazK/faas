"""Bounded, read-only evidence for the fsn-2 compactor drain blocker."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess

INSTANCE = '77057914-6cf9-44ea-9cf1-67dedee53f1f'


def read_command(argv):
    return subprocess.check_output(argv, text=True, timeout=30, stderr=subprocess.DEVNULL)


def safe_message(message):
    lowered = message.casefold()
    if any(marker in lowered for marker in ('postgres://', 'postgresql://', 'password=', 'token=', 'bearer ', 'authorization:')):
        return '[message omitted: credential-like content]'
    return message[:2000]


def main():
    service = read_command(['systemctl', 'show', 'faas-vmmd.service',
                            '--property=ActiveState,SubState,MainPID,ExecMainStartTimestamp,Result'])
    properties = dict(line.split('=', 1) for line in service.splitlines() if '=' in line)
    pid = int(properties.get('MainPID', '0'))
    process = {}
    if pid > 0:
        executable = Path('/proc') / str(pid) / 'exe'
        process['executable'] = os.readlink(executable)
        process['mount_namespace'] = os.readlink(Path('/proc') / str(pid) / 'ns/mnt')
        with executable.open('rb') as stream:
            process['sha256'] = hashlib.file_digest(stream, 'sha256').hexdigest()
    release = json.loads(Path('/opt/faas/current/release-manifest.json').read_text())
    journal = read_command(['journalctl', '--unit=faas-vmmd.service', '--since=-1 hour',
                            '--lines=2000', '--output=json', '--no-pager'])
    messages = []
    for line in journal.splitlines():
        record = json.loads(line)
        message = record.get('MESSAGE', '')
        if not isinstance(message, str):
            continue
        if INSTANCE not in message and 'adopt_failed' not in message:
            continue
        messages.append({'timestamp_us': record.get('__REALTIME_TIMESTAMP'),
                         'message': safe_message(message)})
    return {'observed_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
            'instance': INSTANCE, 'service': properties, 'process': process,
            'current_release': release.get('git_sha'), 'messages': messages[-100:]}


if __name__ == '__main__':
    print(json.dumps(main(), indent=2))
