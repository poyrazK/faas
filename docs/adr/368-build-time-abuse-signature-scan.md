# ADR-368 · Build-time abuse signature scan

- **Status:** accepted
- **Date:** 2026-09-29
- **Relates to:** ADR-361 (tenant egress hardening), ADR-101 (image-layer secret scan), ADR-075 (per-deploy grype scan)
- **Context:** ADR-361 limits what a guest can reach and detects scanning and floods at runtime. By the time runtime detection fires, the abuse has already left the platform's egress address. Most abuse on free compute is not sophisticated: an unmodified XMRig release, a masscan binary, or an npm package that drops a miner. The existing post-build scans are the wrong shape to catch these. The secret scan reads only text files up to 1 MiB and skips `node_modules`, where malicious packages live. The grype scan looks up known CVEs, not tools.
- **Decision:**
  1. **Where it runs.** imaged runs a signature scan (`pkg/abusescan`) over the staged app layer on every image deploy. It reuses the staging the secret scan already does (`runDeployLayerScans`), so the layer is mounted once. Sidecars stage the same app layer in the current pipeline and are covered by that pass.
  2. **What it reads.** Every regular file up to 64 MiB, binaries and `node_modules` included. Media, font and archive formats are skipped by extension, because their contents cannot be matched without unpacking. The per-image budget is 2 GiB; hitting it is logged as truncated. Symlinks are not followed. Files are read in 4 MiB chunks that overlap by the longest pattern, so a match across a boundary is still seen.
  3. **Rules.** Each rule is a case-insensitive "all of these, and at least N of those" string match on distinctive markers the real tools embed: option names, protocol methods, Go module paths. A renamed binary still matches.
     - **Block** (fails the deploy with `image_abuse_detected` and names the rule and file):
       - `miner-xmrig`: the `xmrig` marker plus an algorithm, stratum or donate marker;
       - `miner-stratum`: two stratum protocol markers;
       - `scanner-masscan`, `scanner-zmap`: the tool name plus two of its options;
       - `flood-hping3`, `flood-mhddos`.
     - **Flag** (audit event and metric only, for operator review):
       - `miner-pool-domain`: a known pool domain, which a mining dashboard could legitimately contain;
       - `proxy-server`: the Go module paths of gost, frp, chisel, xray, v2ray and shadowsocks. These have legitimate uses, but a free proxy farm is a common abuse pattern.
     A single generic word ("xmrig" in a README, one "stratum+tcp://" in a dashboard) never blocks on its own.
  4. **Signals.** Every finding is written as a `deployment.abuse_scan` audit event (account, app, deployment, digest, findings) and counted in `imaged_abuse_scan_findings_total{category,action}`. `FaasImageAbuseBlocked` pages on any blocking finding; `FaasImageAbuseFlagged` warns on flagged ones. A blocking finding does not place the ADR-361 account hold by itself: the operator decides after review, and runtime detection holds repeat offenders.
  5. **Failure posture.** A walk error is logged and does not fail the deploy, the same as the secret scan. Only a rule match does.
- **Consequences:**
  - An unmodified miner, mass scanner or flood tool never reaches a VM.
  - The scan adds one read pass over the app layer per deploy, bounded at 2 GiB.
  - Packed, encrypted or downloaded-at-runtime payloads evade it. That is expected; ADR-361's runtime controls (default-deny egress, fan-out and flood detection, the account hold) remain the backstop.
  - Function deploys (`buildFunctionLayer`) are not scanned yet.
  - Rules live in code; updating them is a normal PR.
- **Rejected alternatives:**
  - **ClamAV or YARA.** Both add a host daemon or cgo dependency on every node and signature-update plumbing, for the small set of tools that matter here.
  - **Hash lists of known binaries.** One rebuild evades them.
  - **Scanning inside the builder VM.** The builder runs tenant code and cannot be trusted to report on it.
