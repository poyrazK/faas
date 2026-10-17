"""Use the repository's actual adapter; the fixture contains no customer data."""
from pathlib import Path
import hashlib
import json

repo = Path(__file__).resolve().parents[3]
text = (repo / 'pkg/rootfs/build.go').read_text()
start = text.index('const nodeFunctionAdapter = `') + len('const nodeFunctionAdapter = `')
adapter = text[start:text.index('\n`\n', start)] + '\n'
assert adapter.startswith('// FAAS_PERSISTENT_PROTOCOL_V1\n')
fixture = Path(__file__).resolve().parent / 'fixture'
fixture.mkdir(exist_ok=True)
(fixture / 'node22.js').write_text(adapter)
(fixture / 'package.json').write_text('{"type":"module"}\n')
print(json.dumps({'adapter_sha256': hashlib.sha256(adapter.encode()).hexdigest()}))
