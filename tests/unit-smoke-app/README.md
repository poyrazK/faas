# unit-smoke app

The app the unit lane's critical smoke checks restart and call after every
control-plane unit deploy (`scripts/ci/unit_smoke.py`,
docs/adr/20261122170000-continuous-unit-lane.md, "Critical smoke checks").

One-time setup, on a dedicated smoke account (never a customer's):

```
cd tests/unit-smoke-app
gregale deploy --name unit-smoke
gregale key create unit-lane-smoke --scopes apps:read,env:write,deploy:write,usage:read
gh secret set UNIT_SMOKE_API_KEY --env production-us   # the key
gh variable set UNIT_SMOKE_APP --body unit-smoke
```

The app answers `smoke:<SMOKE_TOKEN>`. Do not change it without changing
the check.
