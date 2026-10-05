# Initialize independent regional control-plane cells

ADR-601 adds a database identity and placement boundary for a fresh regional
fleet. It is an internal foundation for independent control planes. It does
not migrate customer resources between cells or provide a global application
endpoint. Regional VM recovery qualification remains pending on native KVM.

For each cell provision its own PostgreSQL cluster, control-plane host, compute
fleet, credentials, storage routes and regional application domain. Keep the
control-plane database named `faas` for the current local peer-auth renderer.
Use a different UUID for every independent cell and backups tagged with that
UUID. Do not create a new cell by cloning another cell's populated database.

Add the following to that cell's split-box deployment manifest:

```yaml
control_plane_cell:
  id: 4e731672-086f-47ac-9e79-d4fc98f6233b # generate a fresh UUID for your cell
  region: eu-fsn
```

Set `region: eu-fsn` on **every** fleet host, including the control-plane host.
Host zone labels remain optional. Supply that cell's database endpoint in
`postgresql.dsn` and a distinct regional `dns.apps_domain`. The ordinary and
direct PostgreSQL endpoints must identify the same logical database.

Before starting the cell's daemons, export `DATABASE_URL` through the operator's
existing credential delivery path, pointing directly at the intended fresh
database. Then run the release-matching tools:

```sh
migrate
gregalectl cell init --manifest-file cell-eu-fsn.yaml --json
gregalectl cell status --manifest-file cell-eu-fsn.yaml --json
gregalectl manifest validate --file cell-eu-fsn.yaml
gregalectl manifest render --manifest-file cell-eu-fsn.yaml --host fsn-control
```

Render/install each host from the same manifest and restart its daemons through
the normal deployment path. The rendered units carry
`FAAS_CONTROL_PLANE_CELL_ID` and `FAAS_CONTROL_PLANE_CELL_REGION`; hand-managed
services must supply the same pair. Administrative database tools using
`pkg/db.Open` also need the pair after initialization. `cell init|status` use
the explicit operator connection and the manifest pair instead.
The manifest-generated Ansible inventory also carries the pair. Bootstrap's
`control_plane_cell` role installs drop-ins before service roles and clears
stale cell environment values for unbound inventories. This supports the
role-installed service units as well as direct manifest rendering.

Initialization removes the unused migration seed `default-local`; real compute
nodes must register with the cell's region before workloads can run. It creates
the region registry entry when absent and preserves an existing admission
policy. Existing accounts/workloads, adopted nodes, a used seed, and a different
cell identity all make initialization fail atomically. Repeating initialization
with the same pair is safe. Initialization requires stopped daemons; it cannot
evict connections opened by old, unconfigured binaries.

Missing/partial identity, the wrong database UUID or region, and an unbound
database all fail daemon connection checks. Both ordinary and direct pools
check new connections; configured pools check each checkout. Explicit foreign
workload regions and foreign/unlabelled nodes fail database triggers. Blank
workload compute selection still chooses eligible nodes within this database.
Configure independent storage even for those blank-selection workloads: this
cell identity does not replace the artifact-routing configuration.

For recovery restore the **same cell's** backup and retain its manifest identity.
A wrong-cell restore should fail connection checks. A deliberate byte-for-byte
clone retains the UUID, so UUID checks cannot distinguish it from its source;
keep clones offline and use infrastructure controls to prevent two active
copies. Rebinding populated databases and deleting/downgrading cell identity
are unsupported. Existing manifests without this section continue in shared,
unbound fleet mode.
