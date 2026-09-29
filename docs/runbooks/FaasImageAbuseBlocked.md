# FaasImageAbuseBlocked / FaasImageAbuseFlagged

imaged scanned a built image and matched an ADR-368 abuse signature rule.

## Symptom

- **FaasImageAbuseBlocked (page):** a blocking rule matched: a cryptominer
  (`miner-xmrig`, `miner-stratum`), a mass port scanner (`scanner-masscan`,
  `scanner-zmap`) or a flood tool (`flood-hping3`, `flood-mhddos`). The deploy
  failed with `image_abuse_detected` and never ran.
- **FaasImageAbuseFlagged (warn):** only a flag rule matched: a mining pool
  domain (`miner-pool-domain`) or a proxy/tunnel server (`proxy-server`). The
  deploy continued.

## Check

1. Find the audit event: kind `deployment.abuse_scan`, subject = deployment
   id. Its data names the account, app, image digest, rule and file.
2. For a block: someone deliberately shipped abuse tooling, or a dependency
   was compromised. Check the account's age, plan and other deploys, and look
   for an unfamiliar package under `node_modules` or a vendored binary.
3. For a flag: open the file. A pool domain inside a mining dashboard is
   fine; one in a start-up script is not. A proxy server is suspicious on
   Free and Hobby.

## Recover

- Deliberate abuse: hold the account (`gregale admin abuse-hold place --note
  "<rule> in <file>" <account_uuid>`) and follow
  [tenant-abuse.md](tenant-abuse.md).
- Compromised dependency: tell the customer which package and file matched.
  Their redeploy passes once it's removed.
- False positive: fix the rule in `pkg/abusescan/scan.go` with a test that
  keeps the legitimate file unblocked.
