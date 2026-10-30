#!/usr/bin/env python3
"""Run the pinned Python workflow rules; Pydantic 2.13.4 is a dev-only dependency.

PYTHONPATH=/tmp/casecite-parity-python python3 scripts/parity/workflow_fixtures.py ../casecite
No Python component is used by the application.
"""
import ast
import dataclasses
from datetime import datetime
from enum import Enum
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import types
from typing import Any, Literal

from pydantic import BaseModel, Field, ValidationError, field_validator, model_validator

baseline = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
dest = Path(__file__).resolve().parents[2]
sources = {}


def load(path, names=None, extra=None):
    raw = subprocess.check_output(["git", "show", f"{baseline}:{path}"], cwd=root)
    sources[path] = hashlib.sha256(raw).hexdigest()
    mod = types.ModuleType("oracle_" + Path(path).stem)
    sys.modules[mod.__name__] = mod
    mod.__dict__.update(dict(re=re, json=json, Any=Any, Literal=Literal, Enum=Enum,
        datetime=datetime, BaseModel=BaseModel, Field=Field, ValidationError=ValidationError,
        field_validator=field_validator, model_validator=model_validator,
        dataclass=dataclasses.dataclass, field=dataclasses.field))
    mod.__dict__.update(extra or {})
    nodes = []
    for node in ast.parse(raw).body:
        name = getattr(node, "name", None)
        if isinstance(node, ast.Assign): name = getattr(node.targets[0], "id", None)
        if isinstance(node, ast.AnnAssign): name = getattr(node.target, "id", None)
        if names is not None:
            if name in names: nodes.append(node)
        elif not isinstance(node, (ast.Import, ast.ImportFrom)):
            nodes.append(node)
    tree = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), *nodes], type_ignores=[])
    exec(compile(ast.fix_missing_locations(tree), path, "exec"), mod.__dict__)
    return mod.__dict__


schema_names = {"ReviewFieldType", "_FIELD_KEY_RE", "field_key_from_name", "ReviewFieldSpec", "_unique_field_keys", "DeepResearchTool",
    "WorkflowStepKind", "WorkflowInputSource", "WorkflowStepInputs", "ExtractFieldsParams", "AnalyzeContractParams", "CompareContractsParams", "DraftDocumentParams", "DeepResearchParams", "CitationCheckParams", "APPROVER_GROUPS", "ApprovalParams", "STEP_PARAM_MODELS", "_STEP_NAME_RE", "TEMPLATE_REF_RE", "CONDITION_OPS", "_UNARY_OPS", "WorkflowCondition", "WorkflowStep", "_template_refs", "referenced_steps_by_field", "_referenced_steps", "validate_workflow_steps", "WorkflowCreateRequest", "WorkflowStepIssue"}
schema = load("backend/app/models/schemas.py", schema_names)
validation = load("backend/app/services/workflows/validation.py", extra=schema)
substitution = load("backend/app/services/workflows/substitution.py", extra={"TEMPLATE_REF_RE": schema["TEMPLATE_REF_RE"]})
conditions = load("backend/app/services/workflows/conditions.py", extra={"_walk": substitution["_walk"]})
templates = load("backend/app/services/workflows/templates.py", extra={k: schema[k] for k in ("WorkflowCreateRequest", "WorkflowStep")})
caps = load("backend/app/services/workflows/outputs.py")


def step(name="first", kind="analyze_contract", params=None, **kw):
    return dict(name=name, kind=kind, params=params or {}, **kw)


cases = [[step()], [step(kind="approval")], [step(kind="citation_check")], [step(kind="compare_contracts")],
    [step(kind="extract_fields", params={"fields": [{"name": "Governing Law"}, {"name": "Duration", "type": "duration"}]})],
    [step(kind="draft_document", params={"instructions": "Draft an NDA."})],
    [step(kind="deep_research", params={"question": "Is assignment permitted?"})],
    [step(kind="approval", params={"approvers": [" u1 ", "u1", "u2"]})],
    [step(kind="compare_contracts", params={"baseline_document_id": "doc"})],
    [step(inputs={"source": "documents", "document_ids": ["d1"]})],
    [step("Extract"), step("memo", "draft_document", {"instructions": "Use {{extract.summary}}", "include_outputs_from": ["EXTRACT"]}, inputs={"source": "step", "step": "Extract"}, condition={"step": "extract", "path": " .data.issues. ", "op": "gte", "value": "2"})],
    [], [step(kind="coffee")], [step(kind="extract_fields", params={"fields": []})],
    [step(kind="extract_fields", params={"fields": [{"name": "Term"}, {"name": "term"}]})],
    [step(params={"posture": "aggressive"})], [step(kind="draft_document", params={"instructions": "x"})],
    [step("bad name!")], [step("same"), step("SAME")],
    [step(kind="citation_check", params={"from_steps": ["later"]}), step("later")],
    [step(inputs={"source": "step", "step": "nope"})],
    [step(kind="deep_research", params={"question": "Use {{first}} here"})],
    [step(inputs={"source": "documents"})], [step(inputs={"source": "step"})],
    [step(kind="compare_contracts", params={"baseline": "document"})],
    [step(kind="approval", params={"approvers": []})], [step(kind="approval", params={"approvers": "everyone"})],
    [step(kind="deep_research", params={"question": "What law applies?", "tools": ["shell"]})],
    [step(kind="deep_research", params={"question": "What law applies?", "tools": []})],
    [step("a"), step("b", condition={"step": "a", "op": "gt", "value": "banana"})],
    [step("a"), step("b", condition={"step": "a", "op": "equals"})],
    [step("a"), step("b", condition={"step": "a", "op": "exists", "path": "x[0]"})],
    [step(f"s{i}") for i in range(13)],
]
validations = []
for steps in cases:
    issues = [i.model_dump(mode="json") for i in validation["validate_steps"](steps, 12)]
    result = dict(steps=steps, valid=not issues, issues=issues)
    if not issues:
        result["normalized"] = [s.model_dump(mode="json") for s in schema["WorkflowCreateRequest"](name="Test", steps=steps).steps]
    validations.append(result)

actuals = [None, "", "   ", [], {}, 0, False, True, 3, "NDA", "USD 250,000", "36 months", ["risky", "long"], {"Term": "12 months"}, "📜é"]
condition_cases = []
for actual in actuals:
    for op in schema["CONDITION_OPS"]:
        expected = None if op in ("exists", "not_exists") else ("nda" if op in ("equals", "not_equals", "contains") else 2)
        c = dict(step="review", path="data.value", op=op, value=expected)
        outputs = {"Review": {"summary": "Done", "data": {"value": actual}}}
        condition_cases.append(dict(condition=c, outputs=outputs, expected=conditions["evaluate"](c, outputs)))
for op in schema["CONDITION_OPS"]:
    c = dict(step="unknown", path="data.missing", op=op, value=1)
    condition_cases.append(dict(condition=c, outputs={}, expected=conditions["evaluate"](c, {})))

outputs = {"Extract": {"summary": "Extracted two fields.", "data": {"obj": {"a": [1, 2]}, "items": [None, 0, False, True, {"term": "12 months"}], "blob": "📜" * 15000}}}
renders = []
for text in ["{{extract}}", "{{ Extract.data.items.4.term }}", "{{Extract.data.obj}}", "{{extract.data.items.0}}/{{extract.data.items.1}}/{{extract.data.items.2}}/{{extract.data.items.3}}", "{{extract.data.nope}} {{unknown.summary}} {{extract.data.items.9}}", "{{extract.data.blob}}", "{{__bad__}} {{extract.__class__}} {{extract.data.items.-1}}"]:
    renders.append(dict(text=text, outputs=outputs, expected=dataclasses.asdict(substitution["render_template"](text, outputs))))

builtin = []
for template in templates["TEMPLATES"]:
    builtin.append({**template, "steps": [s.model_dump(mode="json") for s in templates["template_steps"](template)]})
(dest / "server/internal/app/workflow_templates.json").write_text(json.dumps(builtin, ensure_ascii=False, indent=2) + "\n")
fixture = dict(baseline=baseline, source_sha256=sources, validation=validations, conditions=condition_cases, substitution=renders)
(dest / "server/internal/app/testdata/workflows.json").write_text(json.dumps(fixture, ensure_ascii=False, indent=2) + "\n")
print(f"Wrote {len(validations)} validation, {len(condition_cases)} condition and {len(renders)} substitution cases; {len(builtin)} built-in templates.")
