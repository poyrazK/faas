"""Record source, build configuration and paired executable identities."""
import hashlib
import json
import subprocess
from pathlib import Path

ROOT = Path('/diagnostic')
OPERATIONS = ['EnableIteration', 'FindOrInsertEntry', 'FindEntry', 'InsertEntry',
              'DeleteEntry', 'Rehash', 'Resize']
CONFIG_KEYS = ['v8_enable_maglev', 'v8_enable_pointer_compression',
               'v8_enable_sandbox', 'v8_enable_i18n_support', 'node_use_node_snapshot',
               'node_shared', 'node_use_openssl', 'v8_target_arch']


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def runtime(path):
    code = """const keys = JSON.parse(process.argv[1]);
console.log(JSON.stringify({node:process.version,v8:process.versions.v8,
platform:process.platform,arch:process.arch,
variables:Object.fromEntries(keys.map(k=>[k,process.config.variables[k]??null]))}));"""
    return json.loads(subprocess.check_output([str(path), '-e', code,
                                              json.dumps(CONFIG_KEYS)], text=True))


def executable(path):
    info = runtime(path)
    assert info['node'] == 'v22.23.2' and info['platform'] == 'linux'
    assert info['arch'] == 'x64' and info['variables']['v8_enable_maglev'] == 0
    headers = subprocess.check_output(['readelf', '-l', str(path)], text=True)
    assert '/lib/ld-musl-x86_64.so.1' in headers
    data = path.read_bytes()
    labels = [op for op in OPERATIONS
              if f'!is_iterable() [IdentityMapBase::{op}]'.encode() in data]
    return {'sha256': digest(path), 'bytes': path.stat().st_size,
            'runtime': info, 'identity_map_labels': labels}


items = {name: executable(path) for name, path in {
    'official': Path('/usr/local/bin/node'),
    'stock': ROOT / 'bundle/node-stock',
    'labeled': ROOT / 'bundle/node-labeled',
}.items()}
assert not items['official']['identity_map_labels']
assert not items['stock']['identity_map_labels']
assert items['labeled']['identity_map_labels'] == OPERATIONS
assert items['stock']['runtime'] == items['labeled']['runtime']
assert items['official']['runtime'] == items['stock']['runtime'], items
for name in items:
    smoke = json.loads((ROOT / f'{name}-smoke.json').read_text())
    assert smoke['correct_responses'] == 8000 and smoke['workers'] == 4
    items[name]['smoke'] = smoke

print(json.dumps({
    'purpose': 'Diagnostic only: label seven existing V8 checks; preserve conditions and fatal behavior.',
    'source_url': 'https://nodejs.org/dist/v22.23.2/node-v22.23.2.tar.xz',
    'source_sha256': digest(ROOT / 'node-source.tar.xz'),
    'patch_sha256': digest(ROOT / 'identity-map.patch'),
    'configure': ['./configure'], 'make': ['make', '-j2'],
    'compiler': subprocess.check_output(['g++', '--version'], text=True).splitlines()[0],
    'apk_versions': subprocess.check_output(['apk', 'info', '-v'], text=True).splitlines(),
    'executables': items, 'fresh_snapshot_required': True,
    'production_deployed': False,
}, indent=2))
