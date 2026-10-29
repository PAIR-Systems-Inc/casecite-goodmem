#!/usr/bin/env python3
"""Execute pinned upstream review-table logic; no app/runtime dependencies.

This is a development oracle, never a Python service. It also preserves the
four original built-in schemas as data for the Go binary.
"""
import ast
import dataclasses
import hashlib
import json
import logging
import math
from pathlib import Path
import re
import subprocess
import sys
import typing

baseline = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
dest = Path(__file__).resolve().parents[2]
sources = {}

def load(relative, names, env=None):
    raw = subprocess.check_output(["git", "show", f"{baseline}:{relative}"], cwd=root)
    sources[relative] = hashlib.sha256(raw).hexdigest()
    scope = dict(re=re, math=math, Any=typing.Any, dataclass=dataclasses.dataclass,
                 logger=logging.getLogger("oracle"))
    scope.update(env or {})
    selected = []
    for node in ast.parse(raw).body:
        if isinstance(node, (ast.FunctionDef, ast.ClassDef)):
            name = node.name
        elif isinstance(node, ast.Assign) and isinstance(node.targets[0], ast.Name):
            name = node.targets[0].id
        elif isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name):
            name = node.target.id
        else:
            continue
        if name in names:
            selected.append(node)
    tree = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), *selected], type_ignores=[])
    exec(compile(ast.fix_missing_locations(tree), relative, "exec"), scope)
    return scope

keys = load("backend/app/models/schemas.py", {"_FIELD_KEY_RE", "field_key_from_name"})
templates = load("backend/app/services/review_tables/templates.py", {"BUILTIN_PREFIX", "_field", "BUILTIN_SCHEMAS", "builtin_schema_id", "builtin_schemas"}, keys)
constants = load("backend/app/models/review_tables.py", {"CELL_ERROR", "CELL_EXTRACTED", "CELL_NOT_FOUND", "CELL_UNVERIFIED", "REVIEW_APPROVED", "REVIEW_FLAGGED"})
quote = load("backend/app/services/quote_verify.py", {"_normalize_ws", "find_quote_offset"})
draft = load("backend/app/services/contract_analysis/long_drafting.py", {"_WORD_RE", "_STOP", "_stem", "_term_list", "_terms", "_relevance", "_chunk_text"})
budget = load("backend/app/services/contract_analysis/key_terms.py", {"EXCERPT_CHARS", "PREAMBLE_CHARS", "TOTAL_EXCERPT_BUDGET"})
extract = load("backend/app/services/review_tables/extraction.py", {"QUOTE_MAX_CHARS", "_PROMPT_OVERHEAD_TOKENS", "_PER_FIELD_TOKENS", "TYPE_GUIDANCE", "CellResult", "_field_focus", "_tagged_excerpts", "select_excerpts", "_field_lines", "_cell_from_raw", "estimate_input_tokens"}, {**constants, **quote, **draft, **budget})
export = load("backend/app/services/review_tables/export.py", {"effective_value", "_display_value", "_status_matches", "select_for_export", "summarise"}, constants)

text = '📜 This Agreement is effective as of January 5, 2026.\nGOVERNING LAW. This Agreement shall be governed by the laws of the State of Delaware.\nPayment is due\n\twithin 30 days of invoice.'
quotes = ["This Agreement shall be governed by the laws of the State of Delaware.", "Payment is due within 30 days of invoice.", "Not in this document.", "", "30 days", "x"]
cell_cases = []
for raw in [None, {}, {"not_found": True, "value": "ignored"}, *[{"value": "Delaware", "quote": q} for q in quotes], {"value": "v" * 1200, "quote": "nope"}]:
    result = dataclasses.asdict(extract["_cell_from_raw"](text, raw))
    for k in ("span_start", "span_end"):
        if result[k] is not None:
            result[k] = len(text[:result[k]].encode("utf-16-le")) // 2
    cell_cases.append(dict(text=text, raw=raw, expected=result))
fields = templates["builtin_schemas"]()[0]["fields"]
long_text = "PREAMBLE. " + "\n\n".join(f"Section {i}. Boilerplate about nothing in particular." for i in range(900)) + "\n\nINSURANCE. The Supplier shall maintain insurance coverage of USD 5,000,000."
excerpt_cases = []
for source in (text, long_text):
    for schema in (fields, [{"key": "insurance", "name": "Insurance", "description": "coverage amount", "type": "money"}], [{"key": "none", "name": "the", "description": "and", "type": "text"}]):
        for tags in ([], [{"canonical_slug": "confidentiality", "span_start": 20, "span_end": 120}]):
            excerpt_cases.append(dict(text=source, fields=schema, tags=tags, expected=extract["select_excerpts"](source, schema, tags)))
cells = [dict(document_id="d1", field_key="parties", status="extracted", value="Acme", review_status="approved"), dict(document_id="d2", field_key="effective_date", status="unverified", value="2026-01-05", override_value="2026-02-01", review_status="flagged"), dict(document_id="d3", field_key="parties", status="not_found", value=None, review_status="pending")]
exports = []
for field_keys in (None, ["parties"], ["missing"]):
    for reviewed in (False, True):
        for statuses in (None, ["extracted", "flagged"], ["not_found"]):
            f, c = export["select_for_export"](fields, cells, field_keys=field_keys, reviewed_only=reviewed, statuses=statuses)
            exports.append(dict(fields=fields, cells=cells, field_keys=field_keys, reviewed_only=reviewed, statuses=statuses, expected_fields=f, expected_cells=c, summary=export["summarise"](f, c)))

(dest / "server/internal/app/review_schemas.json").write_text(json.dumps(templates["builtin_schemas"](), indent=2, ensure_ascii=False) + "\n")
(dest / "server/internal/app/testdata/review_tables.json").write_text(json.dumps(dict(baseline=baseline, source_sha256=sources, cells=cell_cases, excerpts=excerpt_cases, exports=exports, estimates=[dict(chars=n, fields=f, expected=extract["estimate_input_tokens"](n, f)) for n in (-1, 0, 100, 30000, 1000000) for f in (1, 40)]), indent=2, ensure_ascii=False) + "\n")
print(f"Wrote {len(cell_cases)} cells, {len(excerpt_cases)} excerpts, {len(exports)} export selections, 10 estimates and 4 schemas")
