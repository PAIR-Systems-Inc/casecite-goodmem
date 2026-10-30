#!/usr/bin/env python3
"""Run the pinned Python chat/strategy schemas and citation mapper (dev only)."""
import ast
from enum import Enum
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import types
from typing import Any, Literal
from pydantic import BaseModel, Field, ValidationError, field_validator

BASELINE = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
sources = {}
def source(path):
    raw = subprocess.check_output(["git", "show", f"{BASELINE}:{path}"], cwd=root)
    sources[path] = hashlib.sha256(raw).hexdigest()
    return ast.parse(raw)

module = types.ModuleType("chat_api_oracle")
sys.modules[module.__name__] = module
env = module.__dict__
env.update(dict(Any=Any, Literal=Literal, Enum=Enum, BaseModel=BaseModel, Field=Field,
                field_validator=field_validator, re=re))
names = {"ConnectorType", "AnalysisMode", "DocumentFilter", "ChatRequest", "StrategyBriefRequest",
         "ConversationExportCitation", "ConversationExportMessage", "ConversationExportRequest"}
nodes = [n for n in source("backend/app/models/schemas.py").body if getattr(n, "name", "") in names]
nodes += [n for n in source("backend/app/routers/chat.py").body if getattr(n,"name", "") in {"_opt_str", "_brief_citation"}]
tree = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), *nodes], type_ignores=[])
exec(compile(ast.fix_missing_locations(tree), "pinned_chat_api", "exec"), env)
cases = []
for kind, model, field, limit in [("chat", "ChatRequest", "query", 100000), ("strategy", "StrategyBriefRequest", "question", 10000)]:
    inputs = [{field: " What notice is required?\t\n"}, {}, {field: None}, {field: 5},
              {field: ""}, {field: " \t\n"}, {field:"Before\x00 after\x7f."},
              {field:"\x00\x01"}, {field:"📜"*limit}, {field:"📜"*(limit+1)}]
    for flag in (["include_citations", "include_documents", "include_case_law"] if kind == "chat" else ["include_case_law"]):
        for value in [False, True, 0, 1, "true", "FALSE", "yes", "no", "on", "off", None, "maybe", 2, [], {}]:
            inputs.append({field:"Notice?",flag:value})
    if kind == "chat":
        for key, values in {"mode":["research","case","document","compliance","strategy","invalid",None,1],
                            "case_law_limit":[1,50,0,51,1.5,"5","5.0"," 5 ","1e1",True,False,None],
                            "session_id":[None,"a-B_12"," a-B_12 ","bad/id","", "\n", "a"*101, 123],
                            "conversation_id":["valid","bad id",None]}.items():
            inputs += [{field:"Notice?",key:value} for value in values]
        for filt in [None,{}, {"document_ids":[]}, {"folder_paths":["/Cases/"]}, {"matter_ids":[]},
                     {"matter_ids":["m"]*201}, {"sources":["local","google_drive"]}, {"sources":["invalid"]},
                     {"document_ids":[1]}, {"include_subfolders":"false"}, {"search_all":"on"}, {"search_all":None}, "bad"]:
            inputs.append({field:"Notice?","document_filter":filt})
        inputs.append({field:"Notice?","_chat_new_session":False,"access_document_ids":["forged"],"unknown":"ignored"})
    else:
        inputs += [{field:"Notice?", "folder_path":value} for value in [None,"", "/Cases/", "/"*1001, 1]]
        inputs += [{field:"Notice?", "document_ids":value} for value in [None, [], ["a","b"], [1], "a"]]
    for value in inputs:
        case = {"kind":kind,"input":value}
        try:
            expected = env[model](**value).model_dump(mode="json")
            case.update(valid=True, expected=expected)
            if not expected[field].strip():
                case.update(valid=False, improvement="Reject empty text after removing control characters.")
                del case["expected"]
        except ValidationError:
            case["valid"] = False
        cases.append(case)

message = {"role":"assistant","text":"Notice is 30 days.","citations":[{"label":"agreement.pdf","quote":"Notice is 30 days.","url":"https://example.test/agreement"}]}
exports = [dict(messages=[message]), dict(messages=[]), {}, dict(messages=None), dict(messages=[None]),
           dict(messages=[{**message,"role":"system"}]),dict(messages=[{**message,"text":None}]),
           dict(messages=[{**message,"text":"x"*200001}]), dict(messages=[{**message,"text":"📜"*200000}]),
           dict(messages=[{**message,"citations":None}]), dict(messages=[{**message,"citations":[None]}]),
           dict(messages=[{**message,"citations":[{"label":"x"}]*201}]), dict(messages=[{"role":"user"}]),
           dict(messages=[{"role":"user"}]*501)]
for key, values in {"title":["",None,1,"x"*301],"format":["md","pdf","docx","html",None]}.items():
    exports += [dict(messages=[message],**{key:value}) for value in values]
for citation in [{}, {"label":""},{"label":" "},{"label":"x"*501},{"label":1},
                 {"label":"x","quote":"q"*5001},{"label":"x","url":"u"*1001},
                 {"label":"x","quote":None,"url":None},{"label":"x","quote":1}]:
    exports.append(dict(messages=[{**message,"citations":[citation]}]))
for value in exports:
    case = dict(kind="export",input=value)
    try: case.update(valid=True,expected=env["ConversationExportRequest"](**value).model_dump(mode="json"))
    except ValidationError: case["valid"]=False
    cases.append(case)

citations = []
for value in [{}, {"id":"d0","source":"notice.pdf","type":"document","confidence":80,"document_id":"doc-a","passage":"Notice is 30 days."},
              {"id":"c1","type":"case_law","source":"Alpha v. Beta","confidence":0.7,"opinionId":111,"cluster_id":1001,"explanation":"Applies the notice condition.","verified":True},
              {"id":"c2","type":"case_law","confidence":None,"opinion_id":"112","url":"https://www.courtlistener.com/opinion/1002/","verified":False}]:
    citations.append({"input":value,"expected":env["_brief_citation"](value,len(citations))})
output = dict(baseline=BASELINE, source_sha256=sources, requests=cases, citations=citations)
dest = Path(__file__).resolve().parents[2]/"server/internal/app/testdata/chat_api.json"
dest.write_text(json.dumps(output,ensure_ascii=False,indent=2)+"\n")
print(f"Wrote {len(cases)} request and {len(citations)} citation fixtures")
