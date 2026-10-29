# ADR-20261029120000: A Compose service another service calls serves requests

- **Status:** accepted
- **Date:** 2026-10-29
- **Owner:** reposcan and reconcile maintainers
- **Amends:** ADR-20261022131852 (its "classified `worker` until its first boot observes a listener" consequence)
- **Decision:** A Compose service without `ports:` is an HTTP service, not a worker, when another service of the repository calls it:
  - another service addresses it by its Docker name in an `http`, `https`, `ws` or `wss` URL in its Compose `environment:` (`API_URL: http://api:8000`), or
  - a service that serves requests lists it in `depends_on` (in Compose or in `.gregale/project.yaml`) and it declares `expose:`, so it listens. The rule repeats, so `web → api → auth` makes both `api` and `auth` HTTP services when each declares `expose:`.

  `depends_on` alone is often only start order, such as a gateway listing its queue worker, so the service it names stays a worker. The plan then warns: `service "worker" publishes no ports and runs as a background worker, though "gateway" lists it in depends_on`, and says to give the caller's environment the service's URL or give the service `expose:` if the caller does call it. A worker's own `depends_on` is never a call: `beat` depending on `worker` is start order. A service nothing calls stays a worker, as before. `expose:` without a caller does not change the class. An explicit class from another detector (a Procfile entry, for example) still wins. The plan shows the result as `class=http` or `class=worker`.

  An app an earlier deploy created as a worker becomes an HTTP service on the next deploy: its class changes, and the worker execution mode the scan gave it (H9-170) is cleared (`reconcile.releaseScanWorkerMode`). A mode its owner set on an app that was not a worker, or any mode other than worker, is kept.
- **Why:** ADR-20261022131852 made a Compose service without `ports:` internal, the way Docker treats it: reachable only by the project's other services. That is how a developer marks an API only the frontend calls. The scan kept classifying such a service as a worker, expecting the first boot to observe its listener and correct the class (ADR-051). That correction never happened. H9-170 (2026-10-13) gave every scanned worker the worker execution mode, and #5138 (2026-10-22) stopped a worker-mode app from ever being reclassified as HTTP. So every internal API in a Compose project ran as a worker:
  - its deploy failed: a worker-mode boot that finds an HTTP listener ends in `execution_mode_conflict` (H14-98). The customer journey `TestMultiServiceProject` hit this on production on 2026-10-29 (`execution mode "worker" conflicts with characterized class "http"`);
  - one that did run never parked, so it ran and was billed around the clock;
  - when it was not running (after a node failure, for example) a call could not wake it, because request wakes refuse workers, and the caller got 503 `wake_failed`;
  - a release set refused calls to it, because a worker cannot be a binding target;
  - pull request previews left it out, so a preview's calls to it reached production or were refused;
  - Free refused the deploy, because Free cannot run workers.
- **Consequences:**
  - Internal APIs park when idle, wake when called, take part in wake-ahead, release sets and previews, and run on Free.
  - A background worker that another service addresses by an `http` URL (a metrics or health endpoint, for example) becomes an HTTP service: it parks when idle instead of running all the time, or fails readiness if nothing listens. The plan shows `class=http`; dropping that variable from the other service keeps it a worker.
  - An internal API that is only listed in `depends_on`, without `expose:`, or that its callers reach by a hard-coded address, stays a worker. The `depends_on` case is warned in the plan; the other is not visible to the scan.
  - Scan reads Compose environment values for this. It keeps only which services they address; values never enter the scan result, as for the env import (ADR-20261023085230).
- **Rejected alternatives:**
  - **Every service without `ports:` serves requests.** Celery, Sidekiq and similar workers never listen, so each would fail readiness on its first deploy.
  - **Every `depends_on` target serves requests.** `depends_on` is often only start order: a gateway that lists its queue worker would turn a working worker into a service that fails readiness, breaking deploys that work today.
  - **`expose:` alone as the signal.** A listener nobody calls gains nothing from waking on calls, and few Compose files declare it without a caller.
  - **Let characterization decide again** by dropping the worker mode for scanned workers. H9-170 added that mode because a worker class without it was never restarted after a node failure; undoing it reopens that bug for real workers.
