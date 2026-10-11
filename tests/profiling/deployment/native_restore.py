#!/usr/bin/env python3
"""Park and restore a disposable Gregale profiling app; emit observed evidence."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import sys
import time
import urllib.parse
import uuid

from run import HTTPFailure, request, timestamp, validate


class Adapter:
    def __init__(self, config):
        validate(config)
        fixture = config['fixtures']['baseline']
        self.deployment = str(uuid.UUID(fixture['deployment_id']))
        self.api_token = os.environ['GREGALE_ACCEPTANCE_TOKEN']
        self.probe_token = os.environ['GREGALE_NATIVE_PROFILE_TOKEN']
        if len(self.probe_token) < 32:
            raise ValueError('native fixture token must contain at least 32 characters')
        self.root = config['api_url'].rstrip('/') + '/v1/apps/' + urllib.parse.quote(fixture['slug'], safe='')
        self.fixture = fixture['url'].rstrip('/')
        self.deadline = time.monotonic() + 150
        self.receipt = {'schema_version': 1, 'source': 'gregale_native_adapter',
                        'started_at': timestamp(), 'status': 'failed',
                        'restored_from_snapshot': False, 'stale_capture_rejected': False}
        self.park_requested = False
        self.wake_requested = False

    def call(self, path, body=None, fixture=False):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError('native acceptance deadline')
        return request((self.fixture if fixture else self.root) + path, body,
                       self.probe_token if fixture else self.api_token,
                       timeout=min(10, remaining))

    def poll(self, action):
        while time.monotonic() < self.deadline:
            try:
                result = action()
            except HTTPFailure as exc:
                if exc.status not in (404, 502, 503, 504):
                    raise
                result = None
            if result:
                return result
            time.sleep(min(2, max(0, self.deadline - time.monotonic())))
        raise TimeoutError('native evidence deadline')

    def stage(self, value):
        self.receipt['stage'] = value

    def timeline(self, wake_id):
        # Reject truncated evidence rather than silently selecting a partial page.
        timeline = self.call('/wakes/' + str(uuid.UUID(wake_id)) + '/timeline?limit=1000')
        if timeline.get('next_cursor'):
            raise ValueError('wake evidence is truncated')
        return timeline.get('events', [])

    def run(self):
        self.stage('preflight')
        before = self.call('/acceptance/profile-state', fixture=True)
        if self.call('/readyz', fixture=True).get('mode') != 'baseline':
            raise ValueError('native endpoint must serve baseline workload')
        if not re.fullmatch(r'[a-f0-9]{32}', before.get('epoch', '')):
            raise ValueError('invalid collector epoch')
        if before.get('staged_epoch') != before['epoch']:
            raise ValueError('fixture already contains an earlier staged probe; redeploy it')
        instances = self.call('/instances')
        baseline = [i for i in instances if i['deployment_id'] == self.deployment and i.get('resident')]
        if len(baseline) != 1 or baseline[0]['state'] != 'running' or not baseline[0].get('wake_id'):
            raise ValueError('exactly one running baseline instance is required')
        old = baseline[0]
        self.receipt.update(epoch_before=before['epoch'], boot_id_before=before['boot_id'],
                            probe_sha256=before['probe_sha256'], instance_before=old['id'],
                            wake_before=old['wake_id'], deployment_id=self.deployment)
        self.stage('park')
        self.park_requested = True
        def drained():
            self.call('/park', {})
            return True
        self.poll(drained)
        # Park's API response confirms drain; snapshot provenance is a separate event.
        def park_evidence():
            for event in self.timeline(old['wake_id']):
                data = event.get('data', {})
                if (event['kind'] == 'wake.park_completed' and data.get('instance_id') == old['id']
                        and data.get('deployment_id') == self.deployment and data.get('snapshot_id')
                        and dt.datetime.fromisoformat(event['at'].replace('Z', '+00:00')) >=
                        dt.datetime.fromisoformat(self.receipt['started_at'].replace('Z', '+00:00'))):
                    return {'at': event['at'], 'snapshot_id': data['snapshot_id']}
            return None
        self.receipt['park_evidence'] = self.poll(park_evidence)
        if any(i.get('resident') for i in self.call('/instances')):
            raise ValueError('app still has resident instances after park')
        self.stage('wake')
        wake = self.call('/wake', {})
        self.wake_requested = True
        if wake.get('already_running'):
            raise ValueError('concurrent wake invalidates native acceptance')
        wake_id = str(uuid.UUID(wake['wake_id']))
        self.receipt['wake_after'] = wake_id
        def restore_evidence():
            for event in self.timeline(wake_id):
                data = event.get('data', {})
                if event['kind'] == 'wake.boot_completed':
                    if data.get('method') != 'restore' or data.get('restore_fallback_reason'):
                        raise ValueError('wake cold booted or fell back from restore')
                    return {'at': event['at'], 'method': data['method'],
                            'tier': data.get('tier'), 'instance_id': data['instance_id']}
            return None
        # Newly queued wake timelines may not exist until the scheduler admits it.
        def admitted():
            rows = self.call('/instances')
            return next((i for i in rows if i.get('wake_id') == wake_id and i['state'] == 'running'), None)
        restored = self.poll(admitted)
        if restored['deployment_id'] != self.deployment:
            raise ValueError('baseline must be the app live deployment for explicit wake')
        evidence = self.poll(restore_evidence)
        if evidence['instance_id'] != restored['id']:
            raise ValueError('restore evidence belongs to another instance')
        self.receipt['restore_evidence'] = evidence
        self.receipt['snapshot_created'] = True
        self.stage('collector_resume')
        def probe_ready():
            try:
                return self.call('/acceptance/profile-state', fixture=True)
            except (RuntimeError, OSError):
                return None
        after = self.poll(probe_ready)
        if (after['boot_id'] != before['boot_id'] or after['probe_sha256'] != before['probe_sha256']
                or after.get('staged_epoch') != before['epoch'] or after['epoch'] == before['epoch']
                or not re.fullmatch(r'[a-f0-9]{32}', after['epoch'])):
            raise ValueError('snapshot did not retain the staged process or rotate its epoch')
        self.receipt.update(epoch_after=after['epoch'], boot_id_after=after['boot_id'],
                            instance_after=restored['id'], restored_from_snapshot=True)
        self.stage('stale_capture')
        replay = self.call('/acceptance/stale-profile', {'epoch': before['epoch']}, fixture=True)
        if (replay.get('stale_capture_rejected') is not True or replay.get('bridge_status') != 409
                or replay.get('probe_sha256') != before['probe_sha256']):
            raise ValueError('old epoch probe was not rejected by the bridge')
        self.receipt.update(stale_capture_rejected=True, stale_bridge_status=409,
                            status='passed', stage='completed')

    def recover(self):
        # A failure between park and wake must not deliberately leave the app parked.
        if self.park_requested and not self.wake_requested:
            try:
                request(self.root + '/wake', {}, self.api_token, timeout=10)
                self.receipt['recovery_wake_queued'] = True
            except Exception:
                self.receipt['recovery_wake_queued'] = False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    adapter = None
    try:
        adapter = Adapter(json.loads(Path(args.config).read_text()))
        adapter.run()
    except Exception as exc:
        receipt = adapter.receipt if adapter else {'status': 'failed', 'stage': 'configuration'}
        receipt['error_type'] = type(exc).__name__
        if adapter:
            adapter.recover()
    else:
        receipt = adapter.receipt
    receipt['finished_at'] = timestamp()
    print(json.dumps(receipt))
    return 0 if receipt['status'] == 'passed' else 1


if __name__ == '__main__':
    sys.exit(main())
