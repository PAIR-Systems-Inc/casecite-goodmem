#!/usr/bin/env python3
"""Run the pinned transcript parser, Markdown renderer and filename sanitizer."""
import ast
from dataclasses import dataclass, field
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import types
from typing import Any

BASELINE = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
sources = {}
def source(path):
    raw = subprocess.check_output(["git","show",f"{BASELINE}:{path}"],cwd=root)
    sources[path]=hashlib.sha256(raw).hexdigest()
    return ast.parse(raw)

module=types.ModuleType("conversation_export_oracle")
sys.modules[module.__name__]=module
env=module.__dict__
env.update(dict(dataclass=dataclass,field=field,Any=Any,re=re))
names={"AI_REVIEW_NOTICE","markdown_notice","Citation","Message","_INLINE","_HEADING","_BULLET","_NUMBERED","strip_inline","parse_blocks","_coerce","render_markdown","safe_filename"}
nodes=[]
for path in ["backend/app/services/ai_notice.py","backend/app/services/conversation_export.py"]:
    for n in source(path).body:
        name=getattr(n,"name","")
        if isinstance(n,ast.Assign) and isinstance(n.targets[0],ast.Name): name=n.targets[0].id
        if name in names: nodes.append(n)
tree=ast.Module(body=[ast.ImportFrom(module="__future__",names=[ast.alias(name="annotations")],level=0),*nodes],type_ignores=[])
exec(compile(ast.fix_missing_locations(tree),"pinned_export","exec"),env)
env["_stamp"]=lambda:"2026-10-29 19:00 UTC"
cases=[]
texts=["**Bold** __also bold__ *italic* _italic_ `code` [source](https://example.test)",
       "notice_period section_8 § 2. * yes * keep **unclosed", "été*mot* and 漢_字_ word*italic*word",
       "# Title\n\n## Subheading\r\n- Bullet\n* Another\n• Third\n1. First\n2) Second\nParagraph.",
       "  # Indented heading\n###Title\n####### Too deep\n  ٣. Unicode number\n-\u00a0NBSP bullet\n##\u00a0NBSP heading",
       "***nested*** **a** **b** *one* and *two* [empty]() [](/link)",
       "<tag> & text\n   \nline with   spaces  ", "", "a\n_b_\n*c*"]
for text in texts:
    cases.append(dict(kind="inline",input=text,expected=env["strip_inline"](text)))
    cases.append(dict(kind="blocks",input=text,expected=env["parse_blocks"](text)))
for title in ["Notice review", "Été / 😀 case", "../unsafe\r\n\"title", "___", "---", "", "x"*80, " a   b-- ", "案例"]:
    cases.append(dict(kind="filename",input=title,expected=env["safe_filename"](title,"docx")))
for messages in [[dict(role="user",text="Question?"),dict(role="assistant",text=texts[0])],
                 [dict(role="assistant",text=" A grounded answer. ",citations=[dict(label="Agreement <&>",url="https://example.test",quote=" Clause one.\nClause two. ")])]]:
    cases.append(dict(kind="markdown",input=dict(title="Notice review",messages=messages),expected=env["render_markdown"]("Notice review",messages)))
dest=Path(__file__).resolve().parents[2]/"server/internal/app/testdata/conversation_export.json"
dest.write_text(json.dumps(dict(baseline=BASELINE,source_sha256=sources,cases=cases),ensure_ascii=False,indent=2)+"\n")
print(f"Wrote {len(cases)} transcript export fixtures")
