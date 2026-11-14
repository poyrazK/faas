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

Pin a commit (or a release tag) rather than `main`, so a rebuild installs the
same SDK. Replace `<commit>` with a full commit SHA from the SDK's repository.

## Go

```sh
go get github.com/gregale-dev/gregale-go@<commit>
```

```go
import faas "github.com/gregale-dev/gregale-go"
```

## Python

```sh
pip install 'gregale-sdk @ git+https://github.com/gregale-dev/gregale-python@<commit>'
```

Optional extras use the same reference:

```sh
pip install 'gregale-sdk[realtime] @ git+https://github.com/gregale-dev/gregale-python@<commit>'
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
git clone https://github.com/gregale-dev/gregale-js gregale-js
cd gregale-js && git checkout <commit>
cd packages/sdk-node && npm ci && npm run build && npm pack
# in your project
mkdir -p packages && cp /path/to/gregale-js/packages/sdk-node/gregale-sdk-node-*.tgz packages/gregale-sdk.tgz
npm install ./packages/gregale-sdk.tgz
```

Commit `packages/gregale-sdk.tgz` and the updated `package-lock.json`, so a
deploy installs the same SDK. Import it as `@gregale/sdk-node`:

```js
import { defineWorkflow, serveWorkflows } from '@gregale/sdk-node/workflows';
```

For control-plane API calls, use an independent client per account or agent:

```ts
import { createClient } from '@gregale/sdk-node';

const client = createClient('https://api.example.com', {
  token: process.env.GREGALE_API_KEY,
});
const app = await client.apps.getApp({ slug: 'hello' });
```

The client owns its configuration and transport without changing global fetch.
Existing `FaaSClient` and static services remain available for migration. See
the [Node migration guide](../sdk/node/README.md#independent-clients-and-migration)
for concurrent clients, token rotation, preview APIs, and helper limitations.

## First request and installation verification

After installation, follow the first-request example for your language:
[Go](https://github.com/gregale-dev/gregale-go#quick-start),
[Node](https://github.com/gregale-dev/gregale-js/tree/main/packages/sdk-node#first-request),
or [Python](https://github.com/gregale-dev/gregale-python#usage). Give the program
your existing credential through `FAAS_TOKEN`; the account/app examples only
read existing resources. `FAAS_API` optionally selects another API endpoint.

Before updating a public SDK repository, Gregale CI builds its public tree and
runs the README example and typed application/error checks from an independent
consumer. Node is installed from its packed tarball, Python from a wheel in a
fresh virtual environment, and Go from the exported source in a separate
module. This verifies installation without needing a Gregale account; it does
not verify registry publication or deployment availability.

Maintainers with access to the platform's `sdk-mirror` workflow can download its
verified Node tarball or Python wheel, together with a SHA-256 report. See
[SDK publishing](../sdk/README-publishing.md#consumer-installation-gate) for
artifact names and local verification commands.
