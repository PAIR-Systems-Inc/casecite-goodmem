# GoodMem authorization integration

This implements the three follow-ups in
[`GOODMEM_SECURITY_ASSESSMENT.txt`](../GOODMEM_SECURITY_ASSESSMENT.txt). The
assessment is retained as the original finding, not rewritten as a passing audit.

## Credentials and runtime authority

`setup-goodmem` is an explicit administrative operation. It stores its human/root
credential in `GOODMEM_PROVISIONING_FILE`, and publishes a separate mode-0600
runtime JSON only after temporary provisioning grants have been revoked. Setup
is serialized by a file lock; one-time keys and pending grant IDs are saved for
crash recovery. Run provisioning on Unix or through the setup container.

The running Go application uses a dedicated GoodMem service identity. Its scoped
key permits content operations only on spaces it owns, creation of its own spaces,
read access to the base embedding configuration, and retirement of its owned
reader identities. Space policy administration follows ownership; the runtime
has no instance-wide `MANAGE_ACCESS` grant. It can read embedder metadata and call
OCR, but cannot read provider credentials, create users, issue API keys, or read
unrelated private document spaces. Embedding execution is granted to the base
space's models and explicit `GOODMEM_ALLOWED_EMBEDDER_IDS` only. The application
model picker includes only this provisioned allowlist. A configured
reranker receives an exact execution grant.

The base space supplies embedding configuration. New originals are stored in
separate document spaces. Rebuilds with another embedding configuration use
another space for the same document. A document's original, page images, OCR/native
text index and alternate indexes all receive equivalent membership grants.

Each CaseCite user receives a distinct reader service identity. All API reads,
downloads, page rendering and search, including background jobs, use that user's
key. A missing user identity fails closed; there is no fallback to the controller.
The existing SQL access checks, retrieval filters and returned-ID checks remain.

Reader keys have immutable ceilings for `READ_SPACE`, `LIST_MEMORY`, and
`READ_MEMORY`, plus the optional exact reranker execution permission. Their live
content authority consists of `SPACE_VIEWER` assignments on the documents they
can access. The read ceilings use `ANY` so future memberships do not require a
new key; they are **not** per-document immutable ceilings. GoodMem still requires
both live authority and the ceiling. Do not add all-authenticated content grants
or broad roles to CaseCite principals/spaces: GoodMem's additive public-sharing
rules would broaden those boundaries. Trusted GoodMem administrators remain able
to change policy; this is defense against mistaken application filtering, not a
boundary against compromise of the Go process or an upstream administrator.

GoodMem currently allows only humans to issue keys. To keep the application free
of administrative credentials and an extra identity service, setup pre-provisions
a reserve of reader identities (`GOODMEM_IDENTITY_CAPACITY=64` by default).
Unassigned readers have no private content grants. Assignment is serialized in
PostgreSQL and bound permanently through a create-once empty GoodMem space, so a
credential cannot be recycled to another user by importing the runtime file into
another database. Reader keys are encrypted in PostgreSQL with `SECRET_KEY`.

Disabled/deleted users' reader identities are permanently retired. Re-enabling
an account allocates a fresh identity; old keys remain invalid. Setup replenishes
retired slots. Increase capacity and rerun setup before adding more active users
than the reserve accommodates, then restart the application to import the file.
No root key is needed during normal requests or background work.

## Changes, failures and upgrades

Membership changes, document moves/deletes, account disable/delete, ingestion and
space provisioning share a PostgreSQL advisory lock across application replicas.
Access-changing operations revoke affected grants **before** changing local ACLs,
and grant the committed new ACL afterward. An upstream revocation failure prevents
the local mutation. A grant failure can leave legitimate users temporarily without
access; durable pending records are retried by maintenance. No paid model work is
retried by this policy reconciliation. A delayed OCR/rebuild writes into the
document's current space boundary and cannot resurrect a removed membership.
Revocation first resolves any pending grant creation and then revokes its immutable
ID, leaving a GoodMem tombstone. A delayed duplicate creation cannot restore
access. The same ordering protects temporary provisioning grants.

In Compose, `connection:/data` is mounted read-only in `app`.
`provisioning:/provisioning` is mounted only by administrative containers. Neither
provisioning nor the PostgreSQL queue requires an additional running service.
For a native deployment, use a dedicated application OS account and mount only
the runtime file; keep the provisioning directory outside its accessible files.

For an existing installation:

1. Stop all application replicas and back up the coordinated CaseCite and GoodMem
   databases, the stable `SECRET_KEY`, and the connection file.
2. Move the old connection JSON, which contains the administrative key, into the
   separate provisioning directory/volume. Preserve its `base_url`, `space_id`
   and `embedder_id`. Remove it from the runtime mount.
3. Run `setup-goodmem` with `GOODMEM_PROVISIONING_FILE` pointing to that file and
   an output path in the runtime directory. Include all embedding models used by
   existing alternate spaces in `GOODMEM_ALLOWED_EMBEDDER_IDS`.
4. With the app still stopped, run `casecite migrate-goodmem-security` using
   `DATABASE_URL`, `SECRET_KEY`, `GOODMEM_CONFIG_FILE`, `PUBLIC_URL` and the
   administrative `GOODMEM_PROVISIONING_FILE`. Mount the provisioning volume only
   in this one-off container. Set `DEBUG=true` only for local HTTP deployments.
5. Start the normal application without the administrative environment/mount.

Migration creates isolated spaces, copies originals and existing derived indexes,
verifies original checksums and copied bytes, then switches database references
transactionally. Application document IDs/citations remain stable. Cached OCR
follows the copied original without another OCR call. Old shared-space copies are
deleted after cutover. Checkpoints survive uncertain replies and process crashes;
rerun the command to finish. Startup rejects legacy documents and unfinished
checkpoints. Re-embedding copied memories can incur provider costs. Documents
whose OCR index never existed retain their original and report that indexing must
be retried. Interrupted old indexing jobs are cancelled rather than published
against obsolete memory IDs.

Changing allowed embedding models or the reranker can require new immutable
ceilings. Stop the application, rerun setup with the complete desired allowlist,
and restart with the new runtime file. Preserve coordinated backups of both
databases and both configuration files. An independent fork/restored environment
must get its own controller/readers; do not run divergent databases against a
copied credential pool. Out-of-band SQL or GoodMem policy edits are administrative
operations, not supported membership-change APIs.

## Verification

`scripts/integration.sh` runs `TestGoodMemSecurityLive` against the pinned real
GoodMem release and then runs the built application's browser tests. The security
test directly calls GoodMem using reader keys, with no filter and with an incorrect
filter. It checks allowed retrieval and forbidden originals, page images, OCR
indexes, alternate spaces, multi-space retrieval, removed members, moved/deleted
documents, disabled readers and delayed OCR writes. It also checks the runtime's
inability to read another application's private document or issue API keys,
revocation and space-creation outages, late grant creation, repeated provisioning,
migration, and restart. Embedding/OCR responses in this test are deterministic fixtures; these are authorization tests,
not OCR or retrieval-quality benchmarks.

Reference: [GoodMem security model](https://docs.goodmem.ai/docs/concepts/security-model/).
