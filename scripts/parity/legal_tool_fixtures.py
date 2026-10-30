#!/usr/bin/env python3
"""Execute pinned legal-tool helpers/services against deterministic HTTP data."""
import ast
import asyncio
from contextlib import asynccontextmanager
from datetime import date, datetime
from enum import Enum
import hashlib
import html
import json
import logging
from pathlib import Path
import re
import subprocess
import sys
import time
import types
from typing import Any
from urllib.parse import urlsplit, parse_qs
from pydantic import BaseModel, field_validator

BASELINE="6605cb3474cf0c2894080af216d5fed2a81fc997"
root=Path(sys.argv[1]).resolve(); dest=Path(__file__).resolve().parents[2]
sources={}
class HTTPError(Exception): pass
module=types.ModuleType("legal_tool_oracle")
sys.modules[module.__name__]=module
scope=module.__dict__
scope.update(dict(re=re,html_lib=html,logging=logging,Any=Any,Enum=Enum,BaseModel=BaseModel,field_validator=field_validator,
    date=date,datetime=datetime,time=time,asyncio=asyncio,urlsplit=urlsplit,
    logger=logging.getLogger("legal_tool_oracle"),httpx=types.SimpleNamespace(HTTPError=HTTPError),
    settings=types.SimpleNamespace(courtlistener_api_token="fixture")))
def load(path,names=None):
    raw=subprocess.check_output(["git","show",f"{BASELINE}:{path}"],cwd=root)
    sources[path]=hashlib.sha256(raw).hexdigest()
    nodes=[]
    for n in ast.parse(raw).body:
        if isinstance(n,(ast.Import,ast.ImportFrom)):continue
        name=getattr(n,"name",None)
        if isinstance(n,ast.Assign):name=getattr(n.targets[0],"id",None)
        if isinstance(n,ast.AnnAssign):name=getattr(n.target,"id",None)
        if names is None or name in names:nodes.append(n)
    exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module="__future__",names=[ast.alias(name="annotations")],level=0),*nodes],type_ignores=[])),path,"exec"),scope)

load("backend/app/services/courtlistener_client.py")
load("backend/app/services/courtlistener.py",{"_TAG_RE","_html_to_text"})
for file in ["_models","_helpers","_case_lookup","_precedents","_dockets","_oral_arguments","_trends","service"]:
    load(f"backend/app/services/legal_tools/{file}.py")
assets={"opinion_types":scope["OPINION_TYPE_LABELS"],"jurisdictions":scope["JURISDICTION_COURTS"],
    "reporter_ranks":[{"pattern":p.pattern,"rank":rank} for p,rank in scope["_REPORTER_RANK"]]}
(dest/"server/internal/app/legal_tool_assets.json").write_text(json.dumps(assets,ensure_ascii=False,indent=2)+"\n")
helpers=[]
for fn,values in {
    "strip_marks":[None,"", "<mark>Ariz</mark>.\n Certiorari  denied", "A &amp; B\tC", "No <b>raw</b> HTML"],
    "nested_snippet":[{}, {"snippet":"Root <mark>hit</mark>"}, {"snippet":"Ignored","opinions":[{"snippet":"Nested <mark>hit</mark>"}]}, {"snippet":"Ignored","opinions":[{}]}],
    "order_citations":[[], ["173 L. Ed. 2d 868","129 S. Ct. 1937","556 U.S. 662","2009 U.S. LEXIS 3472"], ["12 F. App'x 34","37 F.4th 1098"], ["100 State 2","2 B.R. 40","3 F. Supp. 3d 10","80 F. 2"], [" A ","",None,"A"]],
    "citation_dict_to_string":[None, " 410 U.S. 113 ", {"volume":410,"reporter":"U.S.","page":"113"}, {"volume":None,"reporter":"","page":""}],
    "_fmt_duration":[None,"",0,-1,65,3661,"72",12.9,"12.9","bad"],
    "courts_for_jurisdiction":["federal","ca9","california","New_York","unknown"],
    "resolve_court_ids":[[],["9th cir","ca9","ninth circuit"],["california","new york","dc","federal"],["11th","3rd circuit","invalid words"]],
}.items():
    for value in values:helpers.append({"function":fn,"input":value,"expected":scope[fn](value)})

base="https://www.courtlistener.com/api/rest/v4"
provider={
    "search_opinions":{"count":42,"next":base+"/search/?cursor=next&q=notice","previous":None,"results":[{
        "cluster_id":77,"id":999,"caseName":"<mark>Notice</mark> v. Example","caseNameShort":"Notice","caseNameFull":"Notice LLC v. Example Inc.",
        "citation":["10 S. Ct. 2","100 U.S. 20"],"court":"Court of Appeals","court_id":"ca9","dateFiled":"2020-03-04","dateArgued":"2020-01-02",
        "docketNumber":"20-01","judge":"A &amp; B","status":"Precedential","suitNature":"Contract","citeCount":12,"absolute_url":"/opinion/77/notice/",
        "opinions":[{"id":55,"snippet":"The <mark>notice</mark> requirement applies."}]}]},
    "search_dockets":{"results":[{"docket_id":8,"id":999,"caseName":"Notice v. Example","docketNumber":"20-01","court":"Court of Appeals","court_id":"ca9",
        "dateFiled":"2020-01-02T00:00:00Z","dateTerminated":"2021-03-04","dateArgued":None,"suitNature":"Contract","cause":"Breach",
        "juryDemand":"Plaintiff","jurisdictionType":"Federal","assignedTo":"Judge Example","referredTo":"Judge Other","party":["Notice LLC","Example Inc."],"attorney":"Attorney One","entry_count":5,"docket_absolute_url":"/docket/8/notice/"}]},
    "search_audio":{"results":[{"id":9,"caseName":"<mark>Notice</mark> v. Example","court":"Court of Appeals","court_id":"ca9","dateArgued":"2020-01-02",
        "docketNumber":"20-01","judge":"Example","duration":3661,"local_path_mp3":"audio/2020/notice.mp3","download_url":"http://example.test/original.mp3","snippet":"<mark>Notice</mark> arguments","absolute_url":"/audio/9/notice/","docket_id":8}]},
    "/clusters/77/":{"id":77,"case_name":"Notice v. Example","case_name_short":"Notice","case_name_full":"Notice LLC v. Example Inc.","sub_opinions":[base+"/opinions/55/",base+"/opinions/56/"],
        "citations":[{"volume":10,"reporter":"S. Ct.","page":"2"},{"volume":100,"reporter":"U.S.","page":"20"}],"docket":base+"/dockets/8/","date_filed":"2020-03-04","date_argued":"2020-01-02","judges":"Example","nature_of_suit":"Contract","posture":"Appeal","syllabus":"Notice is required.","headnotes":"Notice terms.","summary":"Affirmed.","citation_count":12,"precedential_status":"Published","absolute_url":"/opinion/77/notice/"},
    "/opinions/55/":{"id":55,"type":"020lead","author_str":"Example","per_curiam":False,"plain_text":"The notice provision applies.","cluster":base+"/clusters/77/"},
    "/opinions/56/":{"id":56,"type":"040dissent","plain_text":"","html_with_citations":"<p>I respectfully <b>dissent</b>.</p>","cluster":base+"/clusters/77/"},
    "/dockets/8/":{"id":8,"case_name":"Notice v. Example","docket_number":"20-01","court_id":"ca9","date_filed":"2020-01-02","nature_of_suit":"Contract","cause":"Breach","assigned_to_str":"Judge Example","pacer_case_id":"123","absolute_url":"/docket/8/notice/"},
    "/courts/ca9/":{"full_name":"United States Court of Appeals for the Ninth Circuit"},
    "/docket-entries/":{"count":5,"results":[{"entry_number":2,"date_filed":"2020-02-03","description":"","recap_documents":[{"description":"Motion","document_number":"2","page_count":4,"filepath_local":"/recap/8/motion.pdf","is_available":True}]}]},
    "/parties/":{"results":[{"name":"Notice LLC","party_types":[{"name":"Plaintiff"}],"attorneys":[{"name":"Attorney One"}]}]},
}
class Response:
    def __init__(self,data,status=200):self._data=data;self.status_code=status;self.headers={}
    def json(self):return self._data
    def raise_for_status(self):
        if self.status_code>=400:raise HTTPError(str(self.status_code))
class Client:
    async def get(self,url,params=None,**kwargs):
        parsed=urlsplit(url);p=parsed.path.removeprefix("/api/rest/v4");params=params or {k:v[0] for k,v in parse_qs(parsed.query).items()}
        if p=="/search/":
            if params.get("q","").startswith("cites:"):
                return Response(provider["citing_opinions"])
            if params.get("q","").startswith("citation:") or params.get("q") in ("Notice v. Example","Other v. Party"):
                return Response({"results":[]} if "999 U.S. 1" in params["q"] else provider["citation_target"])
            if "filed_after" in params:return Response({"count":{2020:100,2021:120,2022:150,2023:160}.get(int(params["filed_after"][:4]),0)})
            return Response(provider[{"o":"search_opinions","d":"search_dockets","oa":"search_audio"}[params.get("type","o")]])
        return Response(provider[p]) if p in provider else Response({},404)
@asynccontextmanager
async def client_cm(**kwargs):yield Client()
scope["cl_client"]=client_cm
service=scope["LegalToolsService"]()
calls=[("search_cases",["notice",None,20],{}), ("get_case_detail",[77],{}), ("get_case_detail",[55],{"id_kind":"opinion"}),
    ("get_case_detail",[55],{}), ("find_precedents",["notice","federal",5,20],{}), ("search_dockets",["notice","ca9","Notice",20],{}),
    ("get_docket_detail",[8],{}), ("search_oral_arguments",["notice","ca9",50],{}), ("analyze_legal_trend",["notice",2020,2023],{})]
results=[]
for name,args,kwargs in calls:results.append({"function":name,"args":args,"kwargs":kwargs,"expected":asyncio.run(getattr(service,name)(*args,**kwargs))})
load("backend/app/services/courtlistener.py")
load("backend/app/routers/tools.py",{"_citing_case","safe_dump"})
provider["citation_target"]={"results":[{**provider["search_opinions"]["results"][0],"caseName":"Notice v. Example"}]}
provider["citing_opinions"]={"results":[{"cluster_id":80+i,"caseName":name,"citation":["101 U.S. 2"],"court":"Court of Appeals","dateFiled":"2023-01-01T00:00:00Z","absolute_url":f"/opinion/{80+i}/later/","opinions":[{"id":90+i,"snippet":snippet}]} for i,(name,snippet) in enumerate([
    ("Negative v. Example","We overruled <mark>100 U.S. 20</mark>."),
    ("Caution v. Example","We distinguished <mark>100 U.S. 20</mark>."),
    ("Positive v. Example","We followed <mark>100 U.S. 20</mark>."),
    ("Neutral v. Example","Opening summary without the citation."),
])]}
citations=[]
for query in ["100 U.S. 20","999 U.S. 1","Other v. Party","Notice v. Example"]:
    result=asyncio.run(scope["courtlistener_service"].validate_citation(query))
    citations.append({"query":query,"expected":scope["safe_dump"](result)})
fixture={"baseline":BASELINE,"source_sha256":sources,"helpers":helpers,"provider":provider,"calls":results,"citation_calls":citations}
(dest/"server/internal/app/testdata/legal_tools.json").write_text(json.dumps(fixture,ensure_ascii=False,indent=2,default=lambda v:v.model_dump(mode="json") if isinstance(v,BaseModel) else v.isoformat())+"\n")
print(f"Wrote {len(helpers)} helper, {len(results)} complete legal-tool service and {len(citations)} citation API fixtures, with original labels, courts and citation ranks")
