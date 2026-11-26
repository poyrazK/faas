# unit-smoke app

The app the unit lane's critical smoke checks restart and call after every
control-plane unit deploy (`scripts/ci/unit_smoke.py`,
docs/adr/20261122170000-continuous-unit-lane.md, "Critical smoke checks").

It lives on a dedicated internal account, `unit-smoke@gregale.invalid`
(Hobby), never a person's or a customer's. The account and its key are made
on the control plane through the state store, as apid makes them, by
`gregalectl unit-smoke mint-key` (run as `faas-apid`, whose database role is
a `faas_runtime` member). The key has only the smoke scopes: `apps:read`,
`env:write`, `deploy:write`, `usage:read`, and lasts a year.

Set up or rotate (on the control plane, then from a machine with `gh`):

```
sudo -u faas-apid env FAAS_PG_DSN='postgres:///faas?host=/run/postgresql&user=faas_apid' \
  gregalectl unit-smoke mint-key --yes --reason=unit_lane_smoke > /root/unit-smoke.json   # once per rotation
# deploy the app as the smoke account, from a copy of this directory outside the repo:
FAAS_TOKEN="$(jq -r .token unit-smoke.json)" gregale deploy --name unit-smoke
jq -r .token unit-smoke.json | gh secret set UNIT_SMOKE_API_KEY -R poyrazK/faas --env production-us
gh variable set UNIT_SMOKE_APP -R poyrazK/faas --body unit-smoke
shred -u unit-smoke.json
```

The app stays private (protection by default); the check presents the same
key to the gateway as its bearer. It answers `smoke:<SMOKE_TOKEN>`. Do not
change it without changing the check.

First set up 2026-11-23: account `bdc1e270-7bde-4643-b34d-a9d344b3aa39`, key
`e11799ae-2205-488f-ab93-dd662eabf540` (expires 2027-11-23), app at
`https://unit-smoke.gregale.app`.
