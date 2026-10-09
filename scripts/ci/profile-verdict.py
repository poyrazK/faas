#!/usr/bin/env python3
"""Require complete observed evidence before native profiling CI can pass."""
import argparse
import json
import os
from pathlib import Path
import sys


def verdict(directory):
    try:
        report = json.loads((directory / 'acceptance.json').read_text())
        cleanup = json.loads((directory / 'cleanup.json').read_text())
        provenance = json.loads((directory / 'provenance.json').read_text())
        receipt = report['native_restore']['receipt']
        passed = (provenance['source_sha'] == provenance['installed_platform_sha'] and
                  (not os.environ.get('GITHUB_SHA') or provenance['source_sha'] == os.environ['GITHUB_SHA']) and
                  report['status'] == 'passed' and
                  cleanup['status'] == 'deletion_scheduled_and_drained' and
                  receipt['source'] == 'gregale_native_adapter' and receipt['status'] == 'passed' and
                  receipt['snapshot_created'] is True and receipt['restored_from_snapshot'] is True and
                  receipt['stale_capture_rejected'] is True and receipt['stale_bridge_status'] == 409 and
                  receipt['epoch_before'] != receipt['epoch_after'] and
                  receipt['boot_id_before'] == receipt['boot_id_after'] and
                  bool(receipt['probe_sha256']) and bool(receipt['park_evidence']['snapshot_id']) and
                  receipt['restore_evidence']['method'] == 'restore')
        for mode, expected in [('regression', 'regressed'), ('label_loss', 'insufficient_data'),
                               ('sparse', 'insufficient_data')]:
            check = report['checks'][mode]
            routes = check['assessment']['route_checks']
            passed = passed and check['passed'] and check['persistence_verified'] and any(
                r['route'] == 'GET /hot/{id}' and r['status'] == expected for r in routes)
        for mode in ['baseline', 'regression', 'label_loss', 'sparse']:
            window = report['windows'][mode]
            passed = passed and all(window[k] is True for k in (
                'gateway_reconciled', 'collector_reconciled', 'route_filter_verified', 'hotspot_present'))
        passed = passed and report['windows']['post_restore']['gateway_reconciled'] is True
    except (OSError, ValueError, KeyError, TypeError):
        report = {}
        passed = False
    status = 'passed' if passed else 'incomplete' if report.get('status') == 'incomplete' else 'failed'
    result = {'status': status, 'source_sha': os.environ.get('GITHUB_SHA', ''),
              'checks': {mode: {'url': value.get('investigation_url'), 'passed': value.get('passed')}
                         for mode, value in report.get('checks', {}).items()}}
    (directory / 'verdict.json').write_text(json.dumps(result, indent=2))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence', required=True)
    args = parser.parse_args()
    directory = Path(args.evidence)
    directory.mkdir(parents=True, exist_ok=True)
    result = verdict(directory)
    print('Native profiling gate:', result['status'])
    summary = os.environ.get('GITHUB_STEP_SUMMARY')
    if summary:
        with open(summary, 'a') as output:
            output.write('\nNative profiling acceptance: **' + result['status'] + '**.\n\n')
            output.write('The artifact contains scenario metrics, investigation URLs, native evidence and cleanup status.\n')
    return 0 if result['status'] == 'passed' else 1


if __name__ == '__main__':
    sys.exit(main())
