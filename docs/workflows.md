# Workflows

A workflow is a saved, repeatable legal process: an ordered, linear list of
typed steps that run the platform's own operations over a set of documents.
Nothing in a workflow is code. Each step names an operation the platform
already has, the parameters it runs with, and where its documents come from.
A run executes the steps in order as one background job, keeps every step's
output, and can be resumed from the step that failed.

## Model

```
Workflow            name, description, matter, steps: [Step, ...]
  Step              name, kind, params (validated per kind), inputs
    inputs.source   run        – the documents the run was started with
                    documents  – ids fixed in the workflow
                    step       – the documents an earlier step produced, else read
WorkflowRun         the steps as they were when the run started, input documents, status
  WorkflowStepRun   per step: status, output {summary, data, document_ids, produced}, error
```

Step names are identifiers (`extract`, `review`, `memo`): other steps refer to
them by name, in `inputs.step`, in `include_outputs_from` / `from_steps`, and in
`{{step.field}}` templates. A reference may only name an **earlier** step;
saving a workflow that refers to a later or nonexistent step, repeats a name,
uses an unknown kind or fails a kind's parameter validation is refused (422).
A workflow holds at most `WORKFLOW_MAX_STEPS` steps (default 12).

## Step kinds

| Kind | What it runs | Parameters | Output `data` |
|------|--------------|------------|---------------|
| `extract_fields` | A tabular review of the step's documents (`services/review_tables`). Creates a review table the runner owns, named `<workflow> — <step>` (or `table_name`), described as created by the run, then runs it. | `fields` (1–40, as a review table's schema: name, type, optional description), `table_name` | `table_id`, `fields`, `documents`, `rows` (per document, per field: value, status, verified, quote), `values` (per document, per field: value), `counts` |
| `analyze_contract` | The full contract pipeline (`contract_analysis.service.analyze`) on each document, up to `WORKFLOW_MAX_DOCUMENTS_PER_STEP` (25). A document that cannot be read or analysed is recorded in `failures`; the step fails only when every document fails. | `contract_type` (default `auto`), `jurisdiction`, `representing`, `posture` (`balanced` / `strict`), `instructions` | `analyses` (document, analysis id, contract type, issue counts by severity, executive summary, parties, key terms), `failures`, `skipped_document_ids` |
| `compare_contracts` | Clause-by-clause comparison (`contract_analysis.compare.compare_contracts`) of up to three documents against a baseline. | `baseline_document_id` (default: the first input), `focus` | `baseline_document_id`, `comparisons` (labels, overall, clauses with status and verified quotes, difference count), `failures` |
| `draft_document` | A draft (`contract_analysis.drafting.draft_document`) from the instructions after `{{…}}` substitution, with the user's practice profile and playbook prepended as the drafting routes do, the named earlier outputs and up to four reference documents as reference text, and the case-law guard applied. | `instructions` (templated), `include_outputs_from` (earlier step names), `reference_document_ids` (≤ 4) | `title`, `text`, `case_law_removed`, `references`, `template_values_used`, `template_values_missing` |
| `deep_research` | A deep-research run (`services/deep_research`): the templated question is planned, the plan optionally narrowed to `tools`, and run over the step's documents (none → everything the runner may read). The run is persisted like any other and appears in the Research view's history. | `question` (templated), `tools` (`documents`, `case_law`; omit for the planner's choice) | `run_id`, `question`, `answer`, `key_findings`, `open_questions`, `sources`, `verification`, template usage |
| `citation_check` | The treatment-language scan (`validate_citation`) of reporter and Westlaw citations given explicitly plus those found in earlier outputs, up to `WORKFLOW_MAX_CITATIONS_PER_STEP` (10). Not a citator. | `citations`, `from_steps` (earlier step names; omit for all earlier steps) | `citations` (per citation: status, warning level, negative and caution counts), `found_in_steps`, `skipped` |

Every step's output also carries a one-line `summary`, the `document_ids` it
read, and `produced` ids by kind (`review_tables`, `analyses`, `research_runs`).
Work product a step creates belongs to the runner and stays when the workflow
or run is deleted.

## Templates: `{{step.field}}`

The `instructions` of a draft step and the `question` of a research step may
embed values from earlier outputs:

```
{{extract}}                       the step's one-line summary
{{extract.data.values}}           its data, as JSON
{{research.data.answer}}          a nested field
{{review.data.analyses.0.contract_type}}   a list index
```

The lookup is a plain path walk over the earlier step's output (dictionary
keys and list indexes). Nothing is evaluated. A path that does not exist
renders as an empty string and is listed in the step's
`template_values_missing`, so the user sees what was not available. A
substituted value is capped at 12,000 characters.

## Runs

`POST /workflows/{id}/run` takes `document_ids`; with none, and a workflow filed
under a matter, every document filed under that matter is the input. The
route checks once, up front, that the runner can read every input, every
fixed document, baseline and reference the steps name, and that a model is
available; the steps then call the service layer directly as the runner. The
run is one job (202 + `poll_url`), never retried by the queue:

- Each step is marked `running`, then `completed` with its output, or `failed`
  with its error. A failure stops the run: later steps are `skipped` and the run
  is `failed` with the step named in its error.
- Cancelling the job (`DELETE /jobs/{job_id}`) marks the current step failed
  (`Cancelled`) and the run `cancelled`.
- `POST /workflows/{id}/runs/{run_id}/resume` re-submits a failed or cancelled
  run from its first unfinished step. Earlier steps keep their outputs and are
  not re-run; the steps executed are the ones the run started with, even if the
  workflow has been edited since. A run in progress or already completed cannot
  be resumed (400).
- Progress is reported per step (`Step 2/3 (review): Analysing contract 4/12`).

## Access and audit

- Routes need the `contracts.use` permission. The owner, or a member of the
  workflow's matter, reads and runs it; the owner alone edits the steps or
  deletes it; anyone else gets 404, never 403. A member runs with their own key
  and their own document access.
- Audit entries: `workflow_created` (matter, step count, kinds, template),
  `workflow_updated`, `workflow_deleted`, `workflow_run_started` and
  `workflow_run_resumed` (run id, steps, start index, documents, kinds),
  `workflow_run_completed` (counts of steps and produced work product) and
  `workflow_run_failed` (the step error, truncated). Parameters, document text
  and outputs never enter the audit log.
- Tables: `workflows`, `workflow_runs`, `workflow_step_runs` (migration 017).

## Built-in templates

`GET /workflows/templates` lists them; `POST /workflows/from-template/{key}`
makes a workflow of the caller's own from one, which they edit freely.

| Key | Steps |
|-----|-------|
| `nda_intake` | extract NDA terms → full review of each NDA (`contract_type: nda`) → citation check over the review's outputs |
| `lease_review` | extract lease terms → compare every lease against the first → draft a comparison memo attaching the comparison and the extracted terms |
| `due_diligence` | extract deal-relevant terms → deep research on change-of-control and assignment exposure, seeded with the extracted terms → draft a findings memo attaching both |

## What leaves your infrastructure

A workflow calls nothing a step does not already call on its own. The LLM
provider receives what the extract, analyse, compare, draft and research
steps send (see the features' own notes and
[subprocessors.md](subprocessors.md)); CourtListener receives the research
step's case-law queries and the citation-check step's citations. No new
third party is contacted.

Every output a workflow produces is model output. Verify it against the
source documents and current law before relying on it.
