# Builds and framework detection

`gregale deploy` inspects the source tree and selects a versioned framework
profile. Common inputs include Node (`package.json`), Python (`requirements.txt`
or `pyproject.toml`), Go (`go.mod`), and a `Dockerfile`. The inferred profile
records the package manager, start command, port, and health path in the
deployment receipt.

Inspect the decision without changing remote state:

```bash
gregale deploy --dry-run
gregale scan --explain
```

If detection cannot find a runnable profile, add an explicit Dockerfile or a
small `gregale.yaml` hosting override. Services must bind `0.0.0.0` and honor
`$PORT`; localhost-only binds and development servers are rejected before
upload where possible.

Build failures are classified as user, dependency, resource, timeout, or
platform failures. The CLI prints the failing stage and one next action. Use
`gregale deployments`, `gregale deploys status ID`, and `gregale logs APP` to
inspect the retained evidence.
