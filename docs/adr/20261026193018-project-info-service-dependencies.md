# ADR-20261026193018: Project info lists each service and the services it calls

- **Status:** accepted
- **Date:** 2026-10-26
- **Decision:** `GET /v1/projects/{slug}` returns `depends_on` on each workload: the project services that workload declares it calls, deduplicated and sorted. `gregale projects info` prints a services table with each service's name, app, latest deployment status and calls.
  - **Source.** apid reads the names from the app's declared service bindings (`Manifest.ServiceBindings`), which the project apply writes from Compose `depends_on` and `x-gregale-service-policy` (ADR-20261026105459). A workload with no declared bindings returns no field.
  - **API.** `depends_on` is an `omitempty` array on `ProjectWorkloadResponse` in both OpenAPI copies, and the Node and Python clients are regenerated. An older CLI ignores the field.
  - **CLI.** The table follows the existing project summary. A missing name, deployment status or calls list prints `-`. `--json` carries the API field unchanged.
- **Why:** A multi-service project showed only a workload count. To learn which service calls which, a user had to read the Compose file or open each app's bindings, and nothing showed that a service's latest deployment had failed while the others were ready.
- **Consequences:**
  - The list shows declared dependencies only. Under the `project` service policy a service can call any project service without declaring it, and those calls do not appear.
  - The response grows by one short list per workload. apid already loads every app of the project for this response, so no query is added.
- **Rejected alternatives:**
  - **Observed calls from gateway traces.** They show traffic, not intent, miss services that have not been called yet, and would put a trace query in a metadata read.
  - **A separate dependency graph endpoint.** The project response already lists the workloads; a second call would only join the same data.
