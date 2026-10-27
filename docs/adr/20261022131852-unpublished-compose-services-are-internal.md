# ADR-20261022131852: A Compose service without `ports:` is internal

- **Status:** accepted
- **Date:** 2026-10-22
- **Decision:** A new app created for a Compose service that publishes no `ports:` gets `visibility: internal`: no public route, still reachable from the account's services at `<name>.svc.gregale` (ADR-266's internal ingress, available on every plan).
  - **Which services.** Services found by the Compose detector with no `ports:` entry. `expose:` stays internal-only, as it already is. Services with `ports:`, and workloads from every other detector (Dockerfile, Procfile, frameworks), stay public.
  - **Create-only.** Like the plan's auth defaults, visibility is set when the app is created. An existing app keeps the visibility its owner chose, and a later sync never hides or publishes it: `gregale app <slug> --visibility public|internal` changes it.
  - **Plan.** A plan workload carries `visibility: internal` for a new unpublished service and for an existing internal app, and gets no `public_url`. The CLI plan prints `visibility=internal`.
  - **Previews inherit visibility.** A GitHub PR preview (and its dependency previews) is created with its production app's visibility, as previews created through the API already are. Before this change a preview of an internal app got a public URL. Previews created earlier keep theirs until they expire.
  - **Release order.** Gateways already enforce visibility, and an older control plane creates the app public as before, so this needs no gate.
- **Why:** In Docker Compose, a service without `ports:` is reachable only from the project's other services. That is how a developer marks an API that only the frontend calls, or a backend that should never face the internet. Gregale made every HTTP service public, so moving a Compose project here exposed services the developer had deliberately kept private.
- **Consequences:**
  - A new Compose project exposes only what it publishes. A service that should be public but has no `ports:` (one fronted by a reverse proxy configured with labels, for example) needs `ports:` or a one-time `--visibility public`.
  - A Compose service without `ports:` is still classified `worker` by the scan until its first boot observes a listener (ADR-051). Visibility and class are independent.
  - Adding `ports:` to an existing internal app does not publish it. The plan shows the app as internal, so the mismatch is visible before the deploy.
- **Rejected alternatives:**
  - **Making the repository the owner of visibility** (re-deriving it on every sync). It would undo an owner's `--visibility` change on the next push, and a repository change would publish a service without anyone reviewing the plan.
  - **An `x-gregale-visibility` extension in this slice.** `ports:` already expresses the intent; an extension can follow if create-only `ports:` proves insufficient.
  - **Leaving previews public.** A preview runs the same code with the same internal endpoints, so a public preview of an internal service exposes what the owner hid.
