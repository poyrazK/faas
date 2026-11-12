# Install the SDKs

Gregale's application SDKs (code-first workflows, realtime, operations,
feature flags) are not on npm or PyPI yet. Install them from Gregale's SDK
repositories as shown below. Until this page says otherwise, a package on a
public registry under a similar name is not Gregale's.

| Language | Repository | Package |
|---|---|---|
| Go | [gregale-dev/gregale-go](https://github.com/gregale-dev/gregale-go) | `github.com/gregale-dev/gregale-go` |
| Python | [gregale-dev/gregale-python](https://github.com/gregale-dev/gregale-python) | `gregale-sdk` |
| Node | [gregale-dev/gregale-js](https://github.com/gregale-dev/gregale-js) | `@gregale/sdk-node` |

> **Python: do not run `pip install faas_sdk` or `pip install faas-sdk`.**
> That name on PyPI belongs to an unrelated company's package. Gregale's
> Python SDK is distributed as `gregale-sdk` and imported as `faas_sdk`.

There are no release tags yet. Each install below pins what it installs (Go
in `go.mod`, Python by commit, Node as a tarball you commit), so a rebuild
installs the same SDK. Repeat it to upgrade.

## Go

```sh
go get github.com/gregale-dev/gregale-go@latest
```

`go get` writes the exact version to `go.mod` and `go.sum`. Until the SDK is
tagged, that version is a pseudo-version of the newest commit
(`v0.0.0-<date>-<commit>`).

```go
import faas "github.com/gregale-dev/gregale-go"
```

## Python

Look up the newest commit, then install it, replacing `<commit>` with the full
SHA the first command prints:

```sh
git ls-remote https://github.com/gregale-dev/gregale-python refs/heads/main
pip install 'gregale-sdk @ git+https://github.com/gregale-dev/gregale-python@<commit>'
```

Optional extras use the same reference:

```sh
pip install 'gregale-sdk[realtime] @ git+https://github.com/gregale-dev/gregale-python@<commit>'
```

In `requirements.txt` or `pyproject.toml`, use the same requirement without
`pip install`. Import the package as `faas_sdk`:

```python
from faas_sdk.workflows import serve_workflows, workflow
```

## Node

npm cannot install a package from a subdirectory of a Git repository, so
build a tarball once:

```sh
git clone --depth 1 https://github.com/gregale-dev/gregale-js
cd gregale-js/packages/sdk-node
npm ci --ignore-scripts
npm run build
npm pack --ignore-scripts
```

Then add it to your project:

```sh
mkdir -p packages
cp /path/to/gregale-js/packages/sdk-node/gregale-sdk-node-*.tgz packages/gregale-sdk.tgz
npm install ./packages/gregale-sdk.tgz
```

Commit `packages/gregale-sdk.tgz` and the updated `package-lock.json`, so a
deploy installs the same SDK. Import it as `@gregale/sdk-node`:

```js
import { defineWorkflow, serveWorkflows } from '@gregale/sdk-node/workflows';
```

The starters from `gregale init` that use the Node SDK expect the tarball at
the same path, `packages/gregale-sdk.tgz`.
