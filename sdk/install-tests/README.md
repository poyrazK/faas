# SDK consumer installation checks

The `sdk-mirror` workflow verifies each public SDK from a fresh consumer project
before updating its public repository. Node uses a packed `.tgz` installed with
dependency scripts disabled, Python uses a wheel installed in a new virtual
environment, and Go uses an independent module with a replacement pointing to
the exported public tree. Consumers never import SDK source from this checkout.

The checks call the existing localhost-only `sdk/fakeapid` fixture through the
default, generally available/beta client. They verify a typed application read
and the public not-found error. Node also compiles the consumer with strict
TypeScript and imports the browser/workflow subpaths. Python also checks the
workflow import and packaged `py.typed` marker. The full language unit and
contract suites continue to run separately.

Each check also extracts and runs the marked first-request code block from the
public tree's README against the same fixture. Its `FAAS_API` and `FAAS_TOKEN`
are set to the fixture URL and a dummy token. Missing or malformed example
markers fail the check. Editing a README example therefore exercises the
installed SDK, not a separate copy of that example in a source-tree test.

```sh
python3 scripts/sdk_mirror.py build --repo gregale-js --out /tmp/gregale-js-tree
python3 scripts/sdk_install_check.py --repo gregale-js \
  --tree /tmp/gregale-js-tree --out /tmp/gregale-js-install
```

Use new or empty output directories. Repeat with `gregale-go` or
`gregale-python`; all checks need Go for the shared fixture. Node needs Node
22.10+ and npm 10+, and Python needs Python 3.11+ with pip and venv. Invoke the
script with the Python version to test, for example `python3.12`. Dependencies
may be downloaded; no Gregale account, production service or registry write is
used. Temporary consumers and the fixture are removed on success or failure.

Each successful check writes `sdk-install.json` with the package identity,
checks performed and the SHA-256 of its tarball/wheel. CI retains these as
`sdk-install-gregale-js`, `sdk-install-gregale-python` and
`sdk-install-gregale-go` artifacts. Go retains a report only: the replacement
checks source consumption, not a published version tag or module proxy.

The tarball and wheel can be installed into another project without a platform
checkout. Download them from the same successful workflow run, check the digest
against its report, then use `npm install /path/to/package.tgz` or
`python -m pip install /path/to/package.whl`. These are verification artifacts;
the check does not publish them to npm, PyPI or a public release.
