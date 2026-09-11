# guest/ — code that runs *inside* every microVM

- `init/` — static Go PID 1 injected by imaged into every app layer (spec §4.8):
  mounts, brings up eth0 (always 10.0.0.2/30, ADR-009), applies env, execs the
  app as uid 1000, supervises (restart ≤3). Resume hook re-seeds entropy + steps
  the clock post-restore (spec §11 test V6). An execution marker selects the
  one-shot AF_VSOCK listener instead of the app supervisor; it accepts one
  bounded request, runs the sanitized interpreter as uid 1000, and powers off.
- `executor/` — source-at-rest-free Node 22/24 and Python 3.12/3.13 adapters.
  Each request gets a private scratch directory, a fixed environment, a
  process-group kill boundary, and a result file that is removed before the
  guest exits. No dependencies, secrets, network, or multi-file bundles are
  injected.
- `runners/{node22,python312}/` — 15-line HTTP hosts on :8080 that load the
  customer handler behind the identical request/response contract (spec §4.9).

Lands: init at M1/M2, runners at M7.
