#!/usr/bin/env python3
"""Run the pinned Python chat helpers; export behavior and original prompt assets.

No application boot, database, models or third-party Python dependencies required.
"""
import ast
import copy
from collections import defaultdict
from dataclasses import asdict, dataclass, field
from datetime import date, datetime
from enum import Enum
import hashlib
import json
import logging
import math
from pathlib import Path
import re
import subprocess
import sys
from types import SimpleNamespace
from typing import Any

BASELINE = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
destination = Path(__file__).resolve().parents[2] / "server/internal/app"
sources = {}


def source(relative):
    raw = subprocess.check_output(["git", "show", f"{BASELINE}:{relative}"], cwd=root)
    sources[relative] = hashlib.sha256(raw).hexdigest()
    return ast.parse(raw)


def load(relative, names):
    tree = source(relative)
    nodes = [ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0)]
    for node in tree.body:
        name = getattr(node, "name", "")
        if isinstance(node, ast.Assign) and isinstance(node.targets[0], ast.Name):
            name = node.targets[0].id
        if name in names:
            nodes.append(node)
    env = dict(re=re, json=json, Any=Any, Enum=Enum, dataclass=dataclass, field=field,
               date=date, datetime=datetime, math=math, defaultdict=defaultdict,
               logger=logging.getLogger("chat-oracle"),
               QueryIntent=SimpleNamespace(FACTUAL="factual", ANALYTICAL="analytical"))
    exec(compile(ast.fix_missing_locations(ast.Module(body=nodes, type_ignores=[])), relative, "exec"), env)
    return env, tree


routing_names = {"_STRATEGY_PATTERN", "_FIND_AUTHORITY_PATTERN", "_MATTER_LINK_PATTERN",
                 "_AUTHORITY_PATTERN", "_EXHAUSTIVE_AUTHORITY_PATTERN", "_PER_FILE_PATTERN",
                 "_DOC_TARGET_PATTERN", "_SEARCH_FILLER_RE", "heuristic_per_file", "heuristic_route",
                 "distill_search_query", "parse_route_payload", "parse_search_query"}
routing, route_tree = load("backend/app/services/rag/routing.py", routing_names)
intent, intent_tree = load("backend/app/services/rag/intent.py", {"detect_query_intent"})
search, _ = load("backend/app/services/search.py", {
    "BooleanOperator", "QueryToken", "ParsedQuery", "QueryParser", "BM25", "SearchService"})
enhancements, _ = load("backend/app/services/rag/enhancements.py", {
    "_SENTENCE_SPLIT", "_WORD", "_STOPWORDS", "MERGE_RRF_K", "_keywords", "_result_key",
    "merge_search_results", "compress_context_results", "verify_document_citations"})
grounding, ground_tree = load("backend/app/services/rag/claim_grounding.py", {
    "GROUND_TEXT_LIMIT", "GROUND_LEAD_CHARS", "GROUND_WINDOW", "ANSWER_LIMIT", "MAX_CLAIMS",
    "PASSAGE_LIMIT", "SUPPORT_CONTEXT", "_EDGE_JUNK", "MIN_QUOTE_CHARS", "MIN_CONTENT_RATIO",
    "tidy_span", "content_ratio", "line_of", "file_excerpt"})
reasoning, reasoning_tree = load("backend/app/services/rag/citation_reasoning.py", {
    "MAX_TYPE_LEN", "MAX_REASONING_STEPS", "normalize_reasoning_steps", "join_reasoning_by_ref"})
per_file_tree = source("backend/app/services/rag/per_file.py")

assets = {"baseline": BASELINE, "routing_patterns": {
    name: routing[name].pattern for name in sorted(routing_names) if name.startswith("_")}}
for node in ast.walk(intent_tree):
    if isinstance(node, ast.Assign) and isinstance(node.targets[0], ast.Name):
        if node.targets[0].id in ("factual_patterns", "analytical_patterns"):
            assets[node.targets[0].id] = ast.literal_eval(node.value)
route_fn = next(n for n in route_tree.body if getattr(n, "name", "") == "route_query")
prompt = next(n.value for n in route_fn.body if isinstance(n, ast.Assign) and n.targets[0].id == "prompt")
assets["routing_prompt"] = "".join(v.value for v in prompt.values if isinstance(v, ast.Constant))
assets["keyword_stopwords"] = sorted(enhancements["_STOPWORDS"])
assets["query_stopwords"] = sorted(search["QueryParser"].STOP_WORDS)
assets["field_mappings"] = search["QueryParser"].FIELD_MAPPINGS
assets["instructions"] = {}
for tree, names in [(ground_tree, ["build_grounding_prompt", "build_support_prompt"]),
                    (reasoning_tree, ["build_reasoning_prompt"]),
                    (per_file_tree, ["build_map_prompt", "build_reduce_prompt"])]:
    for name in names:
        fn = next(n for n in tree.body if getattr(n, "name", "") == name)
        returned = next(n.value for n in fn.body if isinstance(n, ast.Return))
        assets["instructions"][name] = returned.values[0].value

queries = ["", "find me case law", "Please find case law for café liability", "compare these leases",
           "What did the letter say?", "What is the risk?", "What is brisk?", "when was it signed",
           "An extended request containing more than eight words without trigger phrases",
           "our strategy", "all authorities for this document", "map the citations in our brief",
           "find precedents about our new client", "which one of these renews?",
           "Compare each of these contracts", "fínd precedents on préemption", "getgood案例 law",
           "support our case", "each document", "summarize every agreement", "What statute governs?"]
test_tree = source("backend/tests/test_query_routing.py")
for node in ast.walk(test_tree):
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id in routing_names:
        for arg in node.args:
            if isinstance(arg, ast.Constant) and isinstance(arg.value, str) and not arg.value.startswith("{"):
                queries.append(arg.value)
fixtures = []
for query in dict.fromkeys(queries):
    route, cases = routing["heuristic_route"](query)
    fixtures.append(dict(kind="route", query=query, expected=dict(intent=route, use_case_law=cases,
        per_file=routing["heuristic_per_file"](query), search_query=routing["distill_search_query"](query))))
    fixtures.append(dict(kind="intent", query=query, expected=intent["detect_query_intent"](query)))
for raw in [None, "", "garbage", "null", "[]", "42", '{}',
            '{"intent":"authority_map","use_case_law":true}',
            '{"intent":" RESEARCH ","use_case_law":false,"search_query":"DWI stops"}',
            '{"intent":"ask","search_query":"x"}', '{"intent":"unknown","search_query":"  café  "}']:
    query = "find authority to support our case"
    route, cases = routing["parse_route_payload"](raw, query)
    fixtures.append(dict(kind="payload", raw=raw, query=query, expected=dict(intent=route,
        use_case_law=cases, search_query=routing["parse_search_query"](raw, query))))

parser = search["QueryParser"]()
for query in ["", "What is not covered?", "WHAT IS NOT COVERED", "contract AND fraud NOT tort",
              "alpha OR beta AND gamma -delta", '"willful infringement" court:ca9 after:2020 before:2025-02-28',
              'filename:"my contract.pdf" type:contract source:local', 'Re:Smith at 10:30 https://court.test/a',
              'court:ca9 court:scotus jurisdiction:federal date:2024 year:2024',
              'after:2024/1/2 before:12/31/2025', 'date_from:2024-02-30 date_to:2024-02-29',
              '-42 -éclair NOT CAFE "NOT covered"', 'café:exhibit judge:Smith cite:"12 F.3d 5"',
              'claim NOT the', 'from:1900 to:2100', 'invoice #3:term', 'NOT OR AND', 'éfile -資料']:
    parsed = parser.parse(query)
    expected = asdict(parsed)
    expected["semantic_query"] = parsed.get_semantic_query()
    fixtures.append(dict(kind="query", query=query, expected=expected))

for documents, query in [([], "none"), (["", "!!!", "brief"], "brief"),
    (["rent rent payable monthly", "monthly notice due", "contract rent annual"], "monthly rent"),
    (["café coopération café", "中文法律 terms", "rent payment monthly"], "café 中文法律")]:
    docs = [dict(id=str(i), text=text) for i, text in enumerate(documents)]
    bm25 = search["BM25"]()
    bm25.index_documents(docs)
    fixtures.append(dict(kind="bm25", documents=docs, query=query, expected=bm25.search(query, 100)))

for text in ["One. Two.", "Rent is payable monthly. Birds fly. Rent increases annually! Cats sleep? Notice is due in writing. The sky is blue.",
             "First unrelated sentence!\nSecond rent sentence.\nThird notice sentence? Fourth rent sentence. Fifth note."]:
    for query in ["rent notice", "the with this", "unrelated", "missing"]:
        for count in [2, 4]:
            rows = [dict(text=text, metadata={})]
            fixtures.append(dict(kind="compression", query=query, documents=rows, count=count,
                                 expected=enhancements["compress_context_results"](rows, query, count)))
for answer in ["The tenant must pay rent monthly in advance.", "Review lease_agreement terms.", "No matching source."]:
    for filename, passage in [("lease_agreement.pdf", "The tenant must pay rent monthly in advance."),
                              ("other.pdf", "Quarterly maintenance of elevators by vendor."), ("a.pdf", "")]:
        c = SimpleNamespace(source=filename, passage=passage, was_cited_by_ai=False, notes=None, status="pending")
        before = vars(c).copy()
        enhancements["verify_document_citations"]([c], answer)
        fixtures.append(dict(kind="reference", citation=before, answer=answer, expected=vars(c)))
for count in [0, 1, 3, 10]:
    lists = [[dict(text="A", similarity=.8, metadata=dict(document_id="a", chunk_index=0)),
              dict(text="B", similarity=.3, metadata=dict(document_id="b", chunk_index=0))],
             [dict(text="B", similarity=.9, metadata=dict(document_id="b", chunk_index=0)),
              dict(text="C", similarity=.6, metadata=dict(document_id="c", chunk_index=1))]]
    fixtures.append(dict(kind="merge", lists=lists, count=count,
                         expected=enhancements["merge_search_results"](copy.deepcopy(lists), count)))
for quote in ["", "| --- |", "  | Notice requires 30 days. |  ", "😀 Rent is $3,000.", "---tiny---", "éclair 中文 contract terms"]:
    fixtures.append(dict(kind="tidy", text=quote, expected=grounding["tidy_span"](quote, 0, len(quote)),
                         ratio=grounding["content_ratio"](quote)))
for ranges in [[], [(29000, 31500)], [(9000, 10000), (10500, 11500), (30000, 30500)]]:
    text = "α Notice obligations.\n" * 1600
    fixtures.append(dict(kind="excerpt", repeated_text="α Notice obligations.\n", repeats=1600,
                         ranges=ranges, expected=grounding["file_excerpt"](text, ranges)))
for raw in [None, [], "bad", [None, "bad", {}, {"type":"Fact used", "description":"  Rent is monthly. ","evidence":" Rent due monthly "}],
            [{"type":"x"*50,"description":str(i),"evidence":None} for i in range(8)]]:
    fixtures.append(dict(kind="reasoning", raw=raw, expected=reasoning["normalize_reasoning_steps"](raw)))

def encode(value):
    if isinstance(value, Enum):
        return value.value
    if isinstance(value, (date, datetime)):
        return value.isoformat()
    raise TypeError(type(value))

(destination / "chat_assets.json").write_text(json.dumps(assets, indent=2, ensure_ascii=False)+"\n")
(destination / "testdata/chat_behavior.json").write_text(json.dumps(dict(baseline=BASELINE,
    sources=sources, fixtures=fixtures), indent=2, ensure_ascii=False, default=encode)+"\n")
print(f"Wrote {len(fixtures)} Python chat behavior cases and original routing/search assets")
