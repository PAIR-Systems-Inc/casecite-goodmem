# Upstream parity work

Baseline: Thomas Unise's CaseCite at `6605cb3474cf0c2894080af216d5fed2a81fc997`.
The committed Go/GoodMem core is preserved at tag
`checkpoint/core-goodmem-a74e09e`.

The acceptance requirement is every upstream feature and a UI at least as
capable as the original. The earlier reduced feature scope is superseded.
The original React application and Word add-in are the UI baseline; restoring
their source is not evidence that their backend workflows work.

`upstream-routes.json` inventories the original router contracts. A route is
complete only when its request, response, authorization, persistence, failure,
and relevant asynchronous behavior have been implemented and tested. A route
count alone does not demonstrate parity.

The reference is intended behavior, not a requirement to reproduce upstream
bugs. Fixes and deliberate improvements need regression tests and a recorded
reason. See [verification evidence and intentional differences](evidence.md).
Python oracle fixtures are a development tool; Python is not part of the Go
application's runtime.

Implementation groups:

- Authentication: local accounts, password recovery, Azure SSO, MFA, required
  enrollment, rotating sessions, device authorization, editable role permissions,
  user invitations and offboarding, tamper-evident audit.
- Knowledge base: upload, folders, original files, page images, text extraction,
  citations, ingestion status, cloud pickers, all eight connector families and
  their incremental synchronization and credential management.
- Matters: membership, ownership transfer, activity and shared resources.
- Research: synchronous/queued chat, saved conversations, case-law tools, deep research,
  strategy briefs, judge intelligence, grounded quotations and exports.
- Contracts: deterministic clause analysis, AI review, instructions, drafting,
  comparison, obligations, deadlines, parties, defined terms, and exact tracked
  redlines preserving the original OOXML package and formatting.
- Review tables: templates, typed extraction, source evidence, editing, reruns,
  and exports.
- Workflows: the original templates, node validation and execution, approvals,
  run/resume/retry/skip, rejection, saved versions and restoration.
- Administration: provider settings and policies, personal keys, diagnostics,
  branding, audit, and all original settings panels.
- Word add-in: device sign-in, document analysis, research, exact range matching,
  tracked edits, citation insertion, and deployment manifest.

Verification uses the original frontend and add-in unit suites, Go service and
PostgreSQL integration tests, provider protocol fixtures, document golden
fixtures, and browser tests of the actual restored UI. Live provider tests are
reported separately from fixture tests. UI comparison covers desktop and mobile,
empty/loading/error states, accessibility, and the original workflow screens.

Architecture remains one Go application binary plus GoodMem, sharing a
PostgreSQL instance with separate databases and roles. Sessions, expiring keys,
leases, queues and workflow state use PostgreSQL; Redis is not required.

Status: implementation in progress. Full parity is not yet verified.
