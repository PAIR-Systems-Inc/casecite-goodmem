#!/usr/bin/env python3
"""Run pinned upstream pure functions to produce independent behavior fixtures.

Only the named source functions/data are loaded. Imports that initialize the
Python web app, storage or providers are never executed.
"""
import ast
import dataclasses
from datetime import datetime
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
dest = Path(__file__).resolve().parents[2] / "server/internal/app/testdata/contract_behavior.json"
sources = {}

def load(relative, names, env=None):
    raw = subprocess.check_output(["git", "show", f"{baseline}:{relative}"], cwd=root)
    sources[relative] = hashlib.sha256(raw).hexdigest()
    scope = dict(re=re, math=math, Any=typing.Any, dataclass=dataclasses.dataclass,
                 logger=logging.getLogger("oracle"), datetime=datetime)
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

issues = load("backend/app/services/contract_analysis/issues.py", {
    "VALID_STATUSES", "VALID_SEVERITIES", "_SEVERITY_ORDER", "_MATCHED_TEXT_LIMIT", "_JURISDICTION_NOTE_LIMIT",
    "_STATUS_BY_KIND", "_GENERAL_RISK_SEVERITY", "_GENERAL_IMPORTANCE_SEVERITY", "ABSENCE_SOURCE_KINDS",
    "_severity", "collect_findings", "_norm_key", "_general_findings", "_mechanical_issue", "merge_issues", "is_absence_claim",
})
test_data = load("backend/tests/test_contract_review_issues.py", {"CLAUSE_RESULT", "GENERAL_ANALYSIS"})
findings = issues["collect_findings"](test_data["CLAUSE_RESULT"], test_data["GENERAL_ANALYSIS"])
issue_cases = []
payloads = [None, {}, {"issues": "garbage"}, {"issues": [None, "bad", {"ref": "unknown"}]}]
payloads += [{"issues": [{"ref": f["ref"], "title": "Annotated " + f["name"], "status": status,
             "severity": severity, "span_start": 999999, "span_end": 999999, "why": "Reason", "suggested_language": "Replacement"}]}
            for f in findings for status, severity in [("info", "minor"), ("bogus", "informational"), ("unusual", "critical")]]
payloads += [{"issues": [{"ref": "dev-0", "title": "First"}, {"ref": "dev-0", "title": "Duplicate"}]}]
for i, payload in enumerate(payloads):
    issue_cases.append({"name": f"merge-{i}", "findings": findings, "payload": payload,
                        "expected": issues["merge_issues"](findings, payload)})

draft = load("backend/app/services/contract_analysis/long_drafting.py", {
    "WORDS_PER_PAGE", "MIN_SECTION_WORDS", "MAX_SECTION_WORDS", "MAX_SECTIONS", "_TOKEN_CHARS",
    "estimate_tokens", "_WORD_RE", "_STOP", "_stem", "_term_list", "_terms", "_relevance", "_chunk_text", "build_reference_block",
    "normalize_plan", "RECONCILE_SECTION_TOLERANCE", "_norm_heading", "_find_heading", "check_reconciled_sections",
    "_HEADING_RE", "_TRAILER_RE", "split_sections", "_QUOTE_RE", "_sections_for_quote",
})
draft_cases = []
for count in (1, 3, 8, 65):
    for words in (0, 50, 700, 4000):
        plan = {"title": "Agreement", "sections": [{"title": f"Section {i}", "target_words": words} for i in range(count)]}
        for target in (0, 9000):
            draft_cases.append({"name": f"plan-{count}-{words}-{target}", "input": plan, "target": target,
                                "expected": draft["normalize_plan"](plan, target or None)})
references = [("Contract", ("Payment is due after acceptance. " * 50) + "\n\n" + ("Termination requires notice and a cure period. " * 45)),
              ("Schedule", "Defined terms apply.\n\n" + "Price and fees are fixed. " * 60)]
reference_cases = [{"name": f"references-{budget}", "references": references, "budget": budget, "focus": "terminate on notice",
                    "expected": draft["build_reference_block"](references, budget, "terminate on notice")} for budget in (0, 50, 600, 5000)]
base = "Agreement\n\n" + "\n\n".join(f"{i}. HEADING {i}\n" + f"Sentence in section {i}. " * 80 for i in range(1, 5)) + "\n\nSIGNATURES\nAuthorized signer.\nEXHIBIT A\nSchedule terms."
split_cases = [{"text": text, "expected": draft["split_sections"](text)} for text in (base, "Only text.", "1. One\nBody\n2. Two\nBody", base.replace("2. HEADING", "2. HEADING", 1))]
sections = [{"number": i, "title": f"Heading {i}", "text": f"{i}. Heading {i}\n" + f"Clause {i} remains unchanged. " * 100} for i in range(1, 4)]
assembled = "Title\n\n" + "\n\n".join(s["text"] for s in sections)
reconcile_cases = []
for text in (assembled, assembled.replace("2. Heading 2", "Missing heading"), assembled.replace(sections[1]["text"], "2. Heading 2\nShort."), assembled + " Extra content." * 100):
    reconcile_cases.append({"text": text, "sections": sections, "expected": draft["check_reconciled_sections"](text, sections)})

classifier = load("backend/app/services/clause_intel/classifier.py", {"Segment", "_HEADING_RE", "segment", "_SUB_ELEMENT_PATTERNS", "detect_sub_elements", "cosine", "keyword_overlap"})
deviations = load("backend/app/services/clause_intel/deviation_detector.py", {"Deviation", "_SEVERITY_BY_SUBELEMENT", "_severity", "_MUTUAL_RE", "_ONE_PARTY_RE", "_mutuality", "_DURATION_RE", "_NUMBER_WORD", "_extract_max_duration_months", "_DOLLAR_CAP_RE", "_FEES_PAID_CAP_RE", "_has_cap_floor", "detect_deviations"})
taxonomy = load("backend/app/services/clause_intel/taxonomy_seed.py", {"CANONICAL_CLAUSES"})["CANONICAL_CLAUSES"]
clause_cases = []
for canonical in taxonomy:
    for text in (canonical["market_standard_text"], "Customer shall perform the service for three years. Provider may terminate immediately without notice."):
        subs = classifier["detect_sub_elements"](text, canonical)
        devs = [dataclasses.asdict(d) for d in deviations["detect_deviations"](canonical, text, subs, 0, len(text), 0.95)]
        for d in devs:
            del d["canonical_id"]  # database-local surrogate is not an API field
        clause_cases.append({"canonical": canonical, "text": text, "sub_elements": subs, "deviations": devs})
segment_texts = [base, "", "A short contract.", "\n\n".join(c["market_standard_text"] for c in taxonomy[:6]),
                 "PREAMBLE\n" + "Preamble words. " * 20 + "\n\n1. TERM\n" + "Term words. " * 20 + "\n\n2. PRICE\n" + "Price words. " * 20]
segments = [{"text": text, "expected": [dataclasses.asdict(s) for s in classifier["segment"](text)]} for text in segment_texts]

guard = load("backend/app/services/rag/case_law_guard.py", {"_regional", "_REPORTERS", "_REPORTER_CITE_RE", "_WL_CITE_RE", "_CONNECTOR", "_WORD", "_GAP", "_PARTY", "_CASE_NAME_RE", "_SINGLE_PARTY_RE", "_LEAD_IN_WORDS", "_normalize", "_Sources", "_merge_spans", "_trim_trailing_punctuation", "find_unverified_case_references"})
guard_cases=[]
for text in ("See Smith v. Jones, 123 F.3d 456. Next sentence.", "In re Acme, 2025 WL 1234.", "The office is at 123 NE 45 Street.", "Ex parte Young controls, according to 209 U.S. 123.", "ARGUMENT\nPer Smith v. Jones the clause controls.", "Doe v. Acme Corp.", "See Board of Education v. Jones, 456 N.E.2d 789."):
    for source in ("",text,"Smith v. Jones, 123 F.3d 456."):
        spans=guard["find_unverified_case_references"](text,None,[{"text":source}])
        guard_cases.append({"text":text,"sources":[source],"expected":[text[a:b] for a,b in spans]})

dest.write_text(json.dumps({"baseline":baseline,"source_sha256":sources,
    "collect_findings":{"clause":test_data["CLAUSE_RESULT"],"general":test_data["GENERAL_ANALYSIS"],"expected":findings},
    "issues":issue_cases,"plans":draft_cases,"references":reference_cases,"split_sections":split_cases,
    "reconcile":reconcile_cases,"clauses":clause_cases,"segments":segments,"guard":guard_cases},indent=2,ensure_ascii=False)+"\n")
print(f"Wrote Python behavior fixtures: {len(issue_cases)} issue merges, {len(draft_cases)} plans, {len(clause_cases)} clauses, {len(guard_cases)} case-law guards")
