#!/usr/bin/env python3
"""Execute pinned Pydantic request validation and document filter methods.

Requires Pydantic 2 for fixture generation only, never at runtime.
"""
import ast
import asyncio
from enum import Enum
import hashlib
import json
import logging
from pathlib import Path
import subprocess
import sys
import types
from typing import Any
from pydantic import BaseModel, Field, ValidationError, field_validator, model_validator

BASELINE = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
dest = Path(__file__).resolve().parents[2]
sources = {}

def source(path):
    raw = subprocess.check_output(["git", "show", f"{BASELINE}:{path}"], cwd=root)
    sources[path] = hashlib.sha256(raw).hexdigest()
    return ast.parse(raw)

module = types.ModuleType("research_api_oracle")
sys.modules[module.__name__] = module
scope = module.__dict__
scope.update(dict(Any=Any, Enum=Enum, BaseModel=BaseModel, Field=Field,
    field_validator=field_validator, model_validator=model_validator,
    logger=logging.getLogger("research_api_oracle")))
names = {"ConnectorType", "DocumentStatus", "DocumentFilter", "DeepResearchTool",
    "DeepResearchSubQuestion", "DeepResearchPlanRequest", "DeepResearchRunRequest"}
nodes = [n for n in source("backend/app/models/schemas.py").body if getattr(n, "name", None) in names]
service = next(n for n in source("backend/app/services/documents.py").body if getattr(n, "name", None) == "DocumentService")
service.body = [n for n in service.body if getattr(n, "name", None) in {"get_documents_by_filter", "_get_folder_path", "_derive_folder"}]
nodes.append(service)
tree = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), *nodes], type_ignores=[])
exec(compile(ast.fix_missing_locations(tree), "pinned_research_api", "exec"), scope)

step = {"id": "edited_1", "question": " What notice is required? ", "tools": ["documents", "documents"]}
base = {"question": " What does the agreement require? ", "plan": [step]}
inputs = [base, {**base, "matter_id": "m1"}, {**base, "plan": []},
    {**base, "plan": [step, step]}, {**base, "plan": [step, {**step, "id": "EDITED_1"}]},
    {**base, "plan": [step, "invalid"]}, {**base, "plan": "not an array"},
    {**base, "question": None}, {**base, "question": "x"}, {**base, "question": "📜" * 4001}]
for patch in [{"id":"bad id"}, {"id":"x"*17}, {"question":"x"}, {"question":"📜"*1001},
    {"tools":[]}, {"tools":["documents","case_law"]}, {"tools":["shell"]}, {"tools":"documents"},
    {"tools":["documents"]*3}, {"tools":[1]}]:
    inputs.append({**base,"plan":[{**step,**patch}]})
for f in [{}, {"document_ids":[]}, {"folder_paths":["/"+"folder"*40+"/"]},
    {"sources":["local"]}, {"sources":["unknown"]}, {"document_ids":[1]}, {"matter_ids":["x"]*201},
    {"include_subfolders":False,"search_all":True}, "not an object"]:
    inputs.append({**base,"document_filter":f})
for value in [False, True, 0, 1, "yes", "FALSE", "on", "off", "maybe", None, 2]:
    inputs.append({**base,"document_filter":{"include_subfolders":value}})
requests=[]
for value in inputs:
    try:
        result=scope["DeepResearchRunRequest"](**value).model_dump(mode="json")
        requests.append({"input":value,"valid":True,"question":result["question"].strip(),"plan":result["plan"]})
    except ValidationError:
        requests.append({"input":value,"valid":False})

docs=[{"id":"a","source":"local","folder_path":None,"metadata":None},
    {"id":"b","source":"google_drive","folder_path":"/Cases/Notice/","metadata":{"doc_type":"brief"}},
    {"id":"c","source":"google_drive","folder_path":None,"metadata":{"parent_folder":"/Cases/Notice/Sub/","doc_type":"contract"}},
    {"id":"d","source":"box","folder_path":None,"metadata":{"path":"/Cases/Other/file.pdf"}}]
instance=scope["DocumentService"]()
instance._can_access=lambda *args:True
instance.documents={d["id"]:types.SimpleNamespace(**{**d,"status":scope["DocumentStatus"].INDEXED,"source":scope["ConnectorType"](d["source"])}) for d in docs}
filters=[{}, {"document_ids":[]}, {"document_ids":["a"]}, {"document_ids":["a"],"folder_paths":["/Cases/Notice/"]},
    {"folder_paths":[]}, {"folder_paths":["/Cases/Notice/"]}, {"folder_paths":["/Cases/Not"]},
    {"folder_paths":["/local/"]}, {"sources":["local"]}, {"sources":["google_drive"]},
    {"doc_types":["brief"]}, {"doc_types":["document"]}, {"sources":[]}, {"doc_types":[]}]
filter_cases=[]
for f in filters:
    for subfolders in (True,False):
        f={**f,"include_subfolders":subfolders}
        expected=asyncio.run(instance.get_documents_by_filter(user_id="owner",**f))
        filter_cases.append({"filter":f,"expected":expected})
output={"baseline":BASELINE,"source_sha256":sources,"requests":requests,"documents":docs,"filters":filter_cases}
(dest/"server/internal/app/testdata/research_api.json").write_text(json.dumps(output,ensure_ascii=False,indent=2)+"\n")
print(f"Wrote {len(requests)} request and {len(filter_cases)} document-filter fixtures")
