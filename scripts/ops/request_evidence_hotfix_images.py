"""Verify exact OCI manifests after the controller binds assets to the signed archive."""
import concurrent.futures
import datetime
import hashlib
import json
from pathlib import Path
import re
import sys
import urllib.parse
import urllib.request

import request_evidence_hotfix as controller

root = Path(sys.argv[1])
tag = sys.argv[2]
assert re.fullmatch(r'v0\.1\.18-rc\.[0-9]+', tag)
label = 'rc' + tag.rsplit('.', 1)[-1]
candidate = {'source_sha': 'cb753d7cd9831a6b1c0547198faf48fa84b3beb7', 'candidate_tag': tag,
             'builder_base_digest': '19aebdedd060588ac3d3ea082ac06e4ed16c405d184e46b52f2883a30b604177',
             'runtime_source_sha': '0a972c6d7bc2056f8b75773cbe2052eab69f9ebc', 'runtime_main_push_publisher_run': 36788689397,
             'builder_source_sha': 'a3e1800e37962a3341ef13703b28a3d3c628191b', 'builder_main_push_publisher_run': 36935598923}
destination = root
# Caller has verified the signature and matched this external manifest to the signed archive.
assert json.loads((destination / 'release-manifest.json').read_text())['git_sha'] == candidate['source_sha']
expected = {
    'MINIMAL': 'base-minimal', 'DEBIAN_PARENT': 'base-debian-parent',
    'NODE22': 'runner-node22', 'PYTHON312': 'runner-python312',
    'GO124': 'runner-go124', 'GO124_ALPINE': 'runner-go124-alpine',
    'NODE24': 'runner-node24', 'PYTHON313': 'runner-python313',
}
sums = dict((line.split()[1].lstrip('*'), line.split()[0])
            for line in (destination / 'SHA256SUMS').read_text().splitlines())
contract = destination / 'runtime-bases.env'
assert hashlib.sha256(contract.read_bytes()).hexdigest() == sums['runtime-bases.env']
refs = {}
for line in contract.read_text().splitlines():
    key, value = line.split('=', 1)
    key = key.removeprefix('FAAS_DEPLOY_BASE_REF_')
    assert key in expected and key not in refs
    match = re.fullmatch(r'ghcr.io/poyrazk/([^@]+)@(sha256:[0-9a-f]{64})', value)
    assert match and match[1] == expected[key]
    refs[key] = (match[1], match[2])
assert set(refs) == set(expected)
signed_manifest = json.loads((destination / 'release-manifest.json').read_text())
production = (destination / 'production-manifest.yaml').read_bytes()
assert 'sha256:' + hashlib.sha256(production).hexdigest() == signed_manifest['manifest_hash']
production_text = production.decode()
builder_digests = re.findall(r'^  builder_base_digest: ([0-9a-f]{64})$', production_text, re.MULTILINE)
assert builder_digests == [candidate['builder_base_digest']]
production_refs = re.findall(r'^    ([a-z0-9_]+): (ghcr.io/poyrazk/[^\s]+)$', production_text, re.MULTILINE)
assert len(production_refs) == 8 and len(dict(production_refs)) == 8
assert dict(production_refs) == {key.lower(): 'ghcr.io/poyrazk/' + image + '@' + digest
                                 for key, (image, digest) in refs.items()}
accept = ','.join(['application/vnd.oci.image.index.v1+json',
                   'application/vnd.docker.distribution.manifest.list.v2+json',
                   'application/vnd.oci.image.manifest.v1+json',
                   'application/vnd.docker.distribution.manifest.v2+json'])


def verify_image(image, selected_digest, source):
    repository = 'poyrazk/' + image
    token_url = 'https://ghcr.io/token?' + urllib.parse.urlencode({
        'service': 'ghcr.io', 'scope': 'repository:' + repository + ':pull'})
    with urllib.request.urlopen(token_url, timeout=45) as response:
        token = json.load(response)['token']

    def fetch(kind, ref):
        request = urllib.request.Request('https://ghcr.io/v2/' + repository + '/' + kind + '/' + ref,
                                         headers={'Authorization': 'Bearer ' + token, 'Accept': accept})
        with urllib.request.urlopen(request, timeout=45) as response:
            data = response.read()
            digest = 'sha256:' + hashlib.sha256(data).hexdigest()
            if ref.startswith('sha256:'):
                assert digest == ref, (image, kind, ref, digest)
            header = response.headers.get('Docker-Content-Digest')
            if header:
                assert header == digest
        # Only public manifests/configs are saved; the anonymous token stays in memory.
        path = root / (label + '-image-' + image + '-' + digest[7:] + '.json')
        path.write_bytes(data)
        return json.loads(data), digest

    tagged, tag_digest = fetch('manifests', 'sha-' + source)
    if 'manifests' in tagged:
        children = [child for child in tagged['manifests']
                    if child.get('platform', {}).get('os') == 'linux'
                    and child.get('platform', {}).get('architecture') == 'amd64']
        assert len(children) == 1, (image, children)
        assert children[0]['digest'] == selected_digest
    else:
        assert tag_digest == selected_digest
    manifest, actual_digest = fetch('manifests', selected_digest)
    assert actual_digest == selected_digest
    config_digest = manifest['config']['digest']
    config, _ = fetch('blobs', config_digest)
    assert config['architecture'] == 'amd64' and config['os'] == 'linux'
    labels = config.get('config', {}).get('Labels', {})
    assert labels.get('org.opencontainers.image.revision') == source, (image, labels)
    assert labels.get('org.opencontainers.image.source') == 'https://github.com/poyrazK/faas', (image, labels)
    return {'image': image, 'source_sha': source, 'source_tag_digest': tag_digest,
            'selected_amd64_digest': selected_digest, 'config_digest': config_digest,
            'manifest_and_config_hashes_verified': True, 'source_labels_verified': True}


with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
    futures = {key: pool.submit(verify_image, image, digest, candidate['runtime_source_sha'])
               for key, (image, digest) in refs.items()}
    builder = pool.submit(verify_image, 'builder-base', 'sha256:' + candidate['builder_base_digest'],
                          candidate['builder_source_sha'])
    runtime = {key: future.result() for key, future in futures.items()}
    builder_result = builder.result()

publishers = {}
for kind in ['runtime', 'builder']:
    run_id = candidate[kind + '_main_push_publisher_run']
    data = controller.api('actions/runs/' + str(run_id))
    assert data['status'] == 'completed' and data['conclusion'] == 'success'
    assert data['head_sha'] == candidate[kind + '_source_sha']
    assert data['head_branch'] == 'main' and data['event'] == 'push'
    publishers[kind] = {'run': run_id, 'head_sha': data['head_sha'],
                        'url': data['html_url'], 'status': data['status'], 'conclusion': data['conclusion']}
receipt = {'at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
           'source_sha': candidate['source_sha'], 'tag': candidate['candidate_tag'],
           'passed': True, 'signed_runtime_contract_sha256': sums['runtime-bases.env'],
           'runtime_and_builder_refs_bound_to_signed_manifest_hash': True,
           'runtime_images': runtime, 'builder_image': builder_result,
           'exact_main_push_publishers': publishers, 'anonymous_registry_reads_only': True}
(root / (label + '-image-manifest-verification.json')).write_text(json.dumps(receipt, indent=2) + '\n')
print(json.dumps({'passed': True, 'runtime_images': len(runtime), 'builder_image': builder_result,
                  'receipt': label + '-image-manifest-verification.json'}, indent=2))
