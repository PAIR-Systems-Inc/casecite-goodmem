#!/usr/bin/env python3
"""Pinned upstream strategy grouping and citation joining, without app startup."""
import ast
import hashlib
import json
from pathlib import Path
import subprocess
import sys
from typing import Any

BASELINE = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
relative = "backend/app/services/strategy.py"
raw = subprocess.check_output(["git", "show", f"{BASELINE}:{relative}"], cwd=root)
tree = ast.parse(raw)
names = {"CHUNKS_PER_DOCUMENT", "SNIPPET_MAX_CHARS", "PASSAGE_MAX_CHARS", "scale_confidence",
         "group_chunks_by_document", "build_passages", "join_citations"}
nodes = []
for node in tree.body:
    name = getattr(node, "name", "")
    if isinstance(node, ast.Assign) and isinstance(node.targets[0], ast.Name):
        name = node.targets[0].id
    if name in names:
        nodes.append(node)
env = dict(Any=Any)
exec(compile(ast.fix_missing_locations(ast.Module(body=nodes, type_ignores=[])), relative, "exec"), env)
fixtures = []
for score in [-1, 0, .005, .015, .025, .135, .445, .795, 1, 2]:
    fixtures.append(dict(kind="confidence", score=score, expected=env["scale_confidence"](score)))
rows = [dict(id=f"c{i}",text=(" " if i==4 else f"Passage {i}. "*45),similarity=.8-i*.1,
             metadata=dict(document_id="a" if i%2==0 else "b")) for i in range(7)]
rows.append(dict(id="orphan",text="Orphan",metadata={}))
for allowed in [[], ["a"], ["b"], ["a","b"]]:
    for count in [1,3,8]:
        grouped = env["group_chunks_by_document"](rows,set(allowed),count)
        fixtures.append(dict(kind="group",rows=rows,allowed=allowed,count=count,expected=grouped))
        order = [(id, f"{id}.pdf") for id in reversed(allowed)]
        fixtures.append(dict(kind="passages",order=order,grouped=grouped,expected=env["build_passages"](order,grouped)))
passages = env["build_passages"]([("a","alpha.pdf"),("b","beta.pdf")],env["group_chunks_by_document"](rows,{"a","b"}))
for data in [{}, {"position":"  Assessment. "}, {"position":"Assessment.","strengths":[{"point":" Point ","refs":["d1","d0","d1","fake"]}],"weaknesses":[{"point":"Risk.","refs":["d0"]}],"next_steps":[{"step":"Read the agreement.","refs":["d2"]}]},
             {"strengths":[None,"bad",{}, {"point":"Unsupported.","refs":[1,"d999"]}]},
             {"weaknesses":"not a list","next_steps":[{"step":"Next.","refs":"d0"}]}]:
    sections,citations=env["join_citations"](data,passages)
    fixtures.append(dict(kind="join",data=data,passages=passages,expected=dict(sections=sections,citations=citations)))
dest=Path(__file__).resolve().parents[2]/"server/internal/app/testdata/strategy_behavior.json"
dest.write_text(json.dumps(dict(baseline=BASELINE,sources={relative:hashlib.sha256(raw).hexdigest()},fixtures=fixtures),indent=2)+"\n")
print(f"Wrote {len(fixtures)} strategy behavior cases")
