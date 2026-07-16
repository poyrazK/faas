# guest/ — code that runs *inside* every microVM

- `init/` — static Go PID 1 injected by imaged into every app layer (spec §4.8):
  mounts, brings up eth0 (always 10.0.0.2/30, ADR-009), applies env, execs the
  app as uid 1000, supervises (restart ≤3). Resume hook re-seeds entropy + steps
  the clock post-restore (spec §11 test V6).
- `runners/{node22,python312}/` — 15-line HTTP hosts on :8080 that load the
  customer handler behind the identical request/response contract (spec §4.9).

Lands: init at M1/M2, runners at M7.
