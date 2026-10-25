# Install the SDKs

Gregale's application SDKs (code-first workflows, application Auth,
realtime, operations, feature flags) are not on npm or PyPI yet. Install them
from the Gregale repository as shown below. Until this page says otherwise, a
package on a public registry under a similar name is not Gregale's.

> **Python: do not run `pip install faas_sdk` or `pip install faas-sdk`.**
> That name on PyPI belongs to an unrelated company's package. Gregale's
> Python SDK is distributed as `gregale-sdk` and imported as `faas_sdk`.

Pin a commit (or a release tag) rather than `main`, so a rebuild installs the
same SDK. Replace `<commit>` with a full commit SHA from
`https://github.com/poyrazK/faas`.

## Go

```sh
go get github.com/poyrazK/faas/sdk/go@<commit>
```

```go
import faas "github.com/poyrazK/faas/sdk/go"
```

## Python

```sh
pip install 'gregale-sdk @ git+https://github.com/poyrazK/faas@<commit>#subdirectory=sdk/python'
```

Optional extras use the same reference:

```sh
pip install 'gregale-sdk[auth] @ git+https://github.com/poyrazK/faas@<commit>#subdirectory=sdk/python'
pip install 'gregale-sdk[realtime] @ git+https://github.com/poyrazK/faas@<commit>#subdirectory=sdk/python'
```

In `requirements.txt`, use the same line without `pip install`. Import the
package as `faas_sdk`:

```python
from faas_sdk.workflows import serve_workflows, workflow
```

## Node

npm cannot install a package from a subdirectory of a Git repository, so
build a tarball once and install it from your project:

```sh
git clone https://github.com/poyrazK/faas gregale-sdk-src
cd gregale-sdk-src && git checkout <commit>
cd sdk/node && npm ci && npm run build && npm pack
# in your project
mkdir -p packages && cp /path/to/gregale-sdk-src/sdk/node/gregale-sdk-node-*.tgz packages/gregale-sdk.tgz
npm install ./packages/gregale-sdk.tgz
```

Commit `packages/gregale-sdk.tgz` and the updated `package-lock.json`, so a
deploy installs the same SDK. Import it as `@gregale/sdk-node`:

```js
import { defineWorkflow, serveWorkflows } from '@gregale/sdk-node/workflows';
```
