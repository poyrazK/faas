#!/usr/bin/env python3
"""Stage prebuilt previews using a site token or explicitly trusted GitHub OIDC."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from urllib.parse import urlsplit
import uuid


def fail(message):
    print('::error::' + message, file=sys.stderr)
    return 1


def canonical_uuid(value):
    try:
        return str(uuid.UUID(value)) == value
    except (ValueError, TypeError, AttributeError):
        return False


def main():
    env = os.environ.copy()
    if env.get('HOSTING_ENABLED') != 'true':
        return fail('Internal frontend hosting Action is disabled; explicitly set enabled: true.')
    token = env.get('FAAS_TOKEN', '')
    oidc = env.get('HOSTING_OIDC_ENABLED', 'false')
    trust = env.get('HOSTING_OIDC_TRUST', '')
    if oidc not in ('true', 'false') or (oidc == 'true' and (token or not canonical_uuid(trust))) or (oidc == 'false' and trust):
        return fail('Select either a site token or explicitly enabled OIDC with a trust UUID; do not mix credentials.')
    if oidc == 'false' and not re.fullmatch(r'fp_host_[0-9a-f]{64}', token):
        return fail('Provide a site-scoped automation credential, not an account or app-deploy token.')
    base = env.get('FAAS_API', '').rstrip('/')
    try:
        parsed = urlsplit(base)
        valid_base = parsed.scheme == 'https' and parsed.hostname and parsed.netloc and not parsed.username and not parsed.password and not parsed.query and not parsed.fragment and not parsed.path and not re.search(r'[\s\x00-\x1f]', base)
        _ = parsed.port
    except ValueError:
        valid_base = False
    if not valid_base:
        return fail('api-base must be a credential-free HTTPS origin.')
    site = env.get('HOSTING_SITE', '')
    if not canonical_uuid(site):
        return fail('site must be a canonical UUID.')
    binary = shutil.which(env.get('HOSTING_CLI', ''))
    if not binary:
        return fail('Install and explicitly select a trusted pinned Gregale CLI with sites ci support.')
    repository, github_run = env.get('GITHUB_REPOSITORY', ''), env.get('GITHUB_RUN_ID', '')
    run = env.get('HOSTING_RUN_ID')
    if not run:
        if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository) or not github_run.isdecimal():
            return fail('Provide run-id outside a GitHub Actions run.')
        run = str(uuid.uuid5(uuid.NAMESPACE_URL, 'gregale-hosting:' + base + '/' + site + ':' + repository + ':' + github_run))
    if not canonical_uuid(run):
        return fail('run-id must be a stable canonical UUID.')
    spa = env.get('HOSTING_SPA', 'false')
    if spa not in ('true', 'false'):
        return fail('spa must be true or false.')
    args = [binary, 'sites', 'ci', '--site', site, '--run-id', run, '--path', env.get('HOSTING_PATH', 'dist'), '--preview-ttl', env.get('HOSTING_PREVIEW_TTL', '24h'), '--wait-timeout', env.get('HOSTING_WAIT_TIMEOUT', '2m')]
    reserve = env.get('HOSTING_RESERVE_PREVIEW', 'false')
    if reserve not in ('true', 'false'):
        return fail('reserve-preview must be true or false.')
    if reserve == 'true':
        args.append('--reserve-preview')
        env['FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED'] = '1'
    if oidc == 'true':
        args.extend(['--oidc-trust', trust])
        env['FAAS_FRONTEND_HOSTING_OIDC_ENABLED'] = '1'
    if spa == 'true':
        args.append('--spa')
    for flag, value in [('--source-repository', repository), ('--source-commit', env.get('GITHUB_SHA', '')), ('--source-branch', env.get('GITHUB_REF_NAME', '')), ('--description', env.get('HOSTING_DESCRIPTION', ''))]:
        if value:
            args.extend([flag, value])
    env.update({'FAAS_API': base, 'FAAS_FRONTEND_HOSTING_ENABLED': '1', 'FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED': '1', 'FAAS_FRONTEND_HOSTING_HISTORY_ENABLED': '1'})
    # argv entries are data. No input is interpolated into shell code. The
    # credential remains in the child's environment, never its arguments.
    try:
        completed = subprocess.run(args, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, check=False)
        result = json.loads(completed.stdout)
    except (OSError, ValueError):
        return fail('CI result unavailable. Inspect this stable run ID before retrying: ' + run)
    if not isinstance(result, dict) or not isinstance(result.get('run'), dict):
        return fail('CI result unavailable; inspect run ' + run + ' before retrying.')
    record = result['run']
    if record.get('id') != run or record.get('site_id') != site:
        return fail('CI result does not match the requested intent.')
    outputs = {'run-id': run, 'publication-state': result.get('state', 'incomplete')}
    for name, field in [('release-id', 'release_id'), ('preview-id', 'preview_id')]:
        value = record.get(field, '')
        if value:
            if not canonical_uuid(value):
                return fail('CI result contains an invalid resource identity.')
            outputs[name] = value
    preview_url = result.get('preview_url', '')
    if preview_url:
        try:
            target = urlsplit(preview_url) if isinstance(preview_url, str) else None
            valid_preview = target is not None and target.scheme == 'https' and not target.username and not target.password and not target.query and not target.fragment and not target.path and target.hostname and re.fullmatch(r'[a-z0-9.-]+', target.netloc) and canonical_uuid(record.get('preview_id')) and target.hostname.startswith('p-' + record['preview_id'] + '.')
        except ValueError:
            valid_preview = False
        if not valid_preview:
            return fail('CI result contains an invalid private preview URL.')
        outputs['preview-url'] = preview_url
    if outputs['publication-state'] not in ('incomplete', 'canceled', 'publication_pending', 'publication_acknowledged'):
        return fail('CI result contains an unknown publication state.')
    if completed.returncode == 0 and (result.get('published') is not True or not preview_url or outputs['publication-state'] != 'publication_acknowledged'):
        return fail('The CLI returned success without publication acknowledgment.')
    output_path = env.get('GITHUB_OUTPUT')
    if output_path:
        with Path(output_path).open('a', encoding='utf-8') as output:
            for key, value in outputs.items():
                output.write(key + '=' + value + '\n')
    # CLI stderr may contain private data. Emit only fixed text and bounded IDs.
    if outputs['publication-state'] == 'canceled':
        return fail('This CI intent was permanently canceled and cannot resume. Inspect run ' + run + ' before explicitly starting another intent.')
    if completed.returncode:
        return fail('CI intent is retained but incomplete. Retry unchanged files with run ID ' + run + '; inspect site state if resources expired or were removed.')
    print('Private preview publication acknowledged for run ' + run + '. Edge refresh and Access sign-in still apply.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
