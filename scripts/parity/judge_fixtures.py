#!/usr/bin/env python3
"""Execute pinned judge helpers and cache queries; no application services needed."""
import ast
import asyncio
import copy
from datetime import UTC, date, datetime, timedelta
import hashlib
import html
import json
import logging
import math
from pathlib import Path
import re
import subprocess
import sys
import types
import unicodedata

BASELINE = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
dest = Path(__file__).resolve().parents[2]
sources = {}
class FixedDateTime(datetime):
    @classmethod
    def now(cls, tz=None):
        return cls(2026, 10, 29, 12, 0, tzinfo=tz)
module = types.ModuleType("judge_oracle")
sys.modules[module.__name__] = module
scope = module.__dict__
scope.update(dict(re=re, math=math, datetime=FixedDateTime, date=date, UTC=UTC,
    timedelta=timedelta, unicodedata=unicodedata, logging=logging, html_lib=html,
    logger=logging.getLogger("judge_oracle")))
class RemoveImports(ast.NodeTransformer):
    def visit_Import(self, node): return None
    def visit_ImportFrom(self, node): return None
def load(path, names=None):
    raw = subprocess.check_output(["git", "show", f"{BASELINE}:{path}"], cwd=root)
    sources[path] = hashlib.sha256(raw).hexdigest()
    nodes = []
    for node in ast.parse(raw).body:
        if isinstance(node, (ast.Import, ast.ImportFrom)): continue
        name = getattr(node, "name", None)
        if isinstance(node, ast.Assign): name = getattr(node.targets[0], "id", None)
        if names is None or name in names: nodes.append(RemoveImports().visit(node))
    tree = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), *nodes], type_ignores=[])
    exec(compile(ast.fix_missing_locations(tree), path, "exec"), scope)

for name in ["_metrics", "_queries", "_wikipedia", "_courtlistener"]:
    load(f"backend/app/services/judge_intel/{name}.py")
load("backend/app/services/courtlistener.py", {"_TAG_RE", "_html_to_text"})
scope["OPINION_TYPE_LABELS"] = json.loads((dest / "server/internal/app/legal_tool_assets.json").read_text())["opinion_types"]
methodology = scope["MetricsMixin"]().get_metrics_methodology()
(dest / "server/internal/app/judge_methodology.json").write_text(json.dumps(methodology, ensure_ascii=False, indent=2)+"\n")

base = "https://www.courtlistener.com/api/rest/v4"
person = {"id":42, "name_first":"Jane", "name_middle":"", "name_last":"Example", "name_suffix":"", "date_dob":"1965-01-02", "dob_city":"Oakland", "dob_state":"CA", "gender":"Female", "race":["Race one","Race two"], "religion":[], "political_affiliation":["Independent"], "aba_rating":"Well Qualified",
    "educations":[base+"/educations/7/"], "positions":[base+"/positions/8/"]}
provider = {
    "/people/42/":person,
    "/educations/7/":{"school":base+"/schools/9/","degree_level":"JD","degree_year":1990},
    "/schools/9/":{"name":"Example University","type":"Law School"},
    "/positions/8/":{"position_type":"Judge","court":base+"/courts/ca9/","appointer":base+"/people/99/","date_start":"2005-01-01","date_termination":None,"termination_reason":None},
    "/courts/ca9/":{"id":"ca9","full_name":"Court of Appeals","short_name":"CA9"},
    "/people/99/":{"id":99,"name_first":"Appointer","name_last":"Example"},
    "/clusters/77/":{"id":77,"sub_opinions":[base+"/opinions/55/",base+"/opinions/56/"]},
    "/opinions/55/":{"id":55,"type":"020lead","author":base+"/people/42/","author_id":42,"author_str":"Jane Example","plain_text":"The notice provision applies.","cluster":base+"/clusters/77/"},
    "/opinions/56/":{"id":56,"type":"040dissent","author":base+"/people/88/","author_id":88,"author_str":"Other Judge","html_with_citations":"<p>I respectfully <b>dissent</b>.</p>","cluster":base+"/clusters/77/"},
    "search_people":{"count":1,"next":None,"results":[person]},
    "search_opinions":{"count":1,"next":None,"results":[{"cluster_id":77,"caseName":"Notice v. Example","caseNameShort":"Notice","court":"Court of Appeals","court_id":"ca9","dateFiled":"2020-03-04","dateArgued":"2020-01-02","docketNumber":"20-01","citation":["100 U.S. 20"],"citeCount":12,"status":"Published","absolute_url":"/opinion/77/notice/","judge":"Jane Example, Other Judge","panel_ids":[42,88],"opinions":[{"id":55,"author_id":42,"type":"lead-opinion","snippet":"The <mark>notice</mark> provision applies."},{"id":56,"author_id":88,"type":"dissent"}]}]},
    "search_dockets":{"count":1,"next":None,"results":[{"docket_id":8,"caseName":"Notice v. Example","court":"Court of Appeals","court_id":"ca9","dateFiled":"2020-01-02","dateTerminated":"2021-03-04","docketNumber":"20-01","suitNature":"Contract","assignedTo":"Jane Example","assignedToId":42,"absolute_url":"/docket/8/notice/","docket_absolute_url":"/docket/8/notice/"}]},
    "/opinions-cited/":{"results":[{"citing_opinion":base+"/opinions/55/","cited_opinion":base+"/opinions/10/","depth":2}]},
}
class Response:
    def __init__(self,data,status=200): self.data=data;self.status_code=status
    def json(self): return copy.deepcopy(self.data)
class Client:
    async def get(self,url,**kwargs):
        path=url.removeprefix(base)
        return Response(provider[path]) if path in provider else Response({},404)
class Service(scope["QueriesMixin"],scope["MetricsMixin"],scope["WikipediaMixin"],scope["CourtListenerMixin"]):
    api_token="";BASE_URL=base;headers={}
    def __init__(self,judge,records): self.row=types.SimpleNamespace(judge_data=judge,opinions_data=records)
    async def _get_cache(self,id): return copy.deepcopy(self.row)
    def _parse_judge_data(self,row): return row.judge_data
    def _parse_opinions_data(self,row): return row.opinions_data
    async def _set_cache(self,id,judge,records): self.row=types.SimpleNamespace(judge_data=copy.deepcopy(judge),opinions_data=copy.deepcopy(records))

education=[{"school_name":"Example University","school_type":"Law School","degree":"JD","degree_year":1990}]
positions=[{"position_type":"Judge","court_name":"Court of Appeals","court_id":"ca9","appointer":"Appointer Example","date_start":"2005-01-01","date_termination":None,"termination_reason":None}]
judge=scope["_build_judge_data"](42,"Jane Example",person,{})
judge.update(education=education,positions=positions)
opinions=[]
for i,cites in enumerate([0,2,9,35,75,150,2]):
    opinions.append({"id":100+i,"case_name":f"Case {i} v. Example","case_name_short":f"Case {i}","court":"Court of Appeals" if i%2==0 else "District Court","date_filed":f"{2018+i}-03-0{1+i}","docket_number":str(i),"citation":f"{i+1} F.4th 1","citation_count":cites,"url":f"https://www.courtlistener.com/opinion/{100+i}/","opinion_type":["020lead","040dissent","030concurrence"][i%3],"full_text":("The court applies the notice provision to this dispute. "*(12+i)) if i<6 else "","word_count":(120+i*10) if i<6 else 0,"snippet":"Search hit"})
dockets=[{"id":i,"case_name":f"Docket {i}","court":"District Court","date_filed":"2020-01-01","date_terminated":f"2020-01-{day:02}","nature_of_suit":"Contract" if i<2 else "Civil Rights","docket_number":str(i),"url":f"https://www.courtlistener.com/docket/{i}/"} for i,day in enumerate([11,21,31])]
records={"opinions":opinions,"dockets":dockets,"citations":[]}
cases=[]
def add(fn,args,expected=None,fix=None):
    if expected is None: expected=scope[fn](*args)
    row={"function":fn,"args":args,"expected":expected}
    if fix: row["intentional_fix"]=fix
    cases.append(row)
for row in [{},person,{"id":5,"name_full":"Name only"},{"id":7,"name_first":"John","name_middle":"G.","name_last":"Roberts","name_suffix":"Jr."}]: add("_judge_summary",[row])
for rows in [[],opinions,opinions*8]:
    add("_dissent_concurrence_metrics",[rows]);add("_citation_impact_metrics",[rows],fix="empty sample is insufficient" if not rows else None)
    add("_writing_complexity_metrics",[rows]);add("_opinion_length_trend_metrics",[rows])
for rows in [[],dockets,dockets[:2],[{"date_filed":"bad","date_terminated":"2020-04-01"},{"date_filed":"2020-01-01","date_terminated":"2019-01-01"}]]:
    add("_docket_durations",[rows]);add("_case_disposition_metrics",[rows],fix="even median averages middle pair" if len(rows)==2 and rows==dockets[:2] else None);add("_case_type_metrics",[rows])
for rows in [[],positions]: add("_bench_experience_metrics",[rows])
for title,name in [("John Roberts","John Roberts"),("Julia Roberts","John Roberts"),("John G. Roberts Jr.","John Roberts"),("Roberts Court","John Roberts"),("José Pérez (judge)","José Pérez"),("J. Smith (judge)","John Smith")]: add("title_matches_judge",[title,name])
for intro,year in [(None,None),("John Smith is an actor.",None),("John Smith (born 1950) is a district judge.",1950),("John Smith (born 1950) is a district judge.",1965),("John Smith is a judge.",1965)]: add("intro_describes_judge",[intro,year])
service=Service(judge,records)
for fn,args in [("get_judge_profile",[42]),("get_statistics",[42])]: add(fn,args,asyncio.run(getattr(service,fn)(*args)))
queries=[]
for query in [{},{"query":"CASE 2"},{"query":"notice"},{"court":"district"},{"year_start":2020,"year_end":2022},{"min_citations":10},{"order":"citations","limit":3,"offset":2},{"offset":100},{"query":"absent"}]:
    queries.append({"query":query,"expected":asyncio.run(service.query_opinions(42,**query))})
text,parts=asyncio.run(service._fetch_cluster_text(Client(),77))
assets={"baseline":BASELINE,"sources_sha256":sources,"now":"2026-10-29T12:00:00Z","helpers":cases,"judge":judge,"records":records,"queries":queries,"cluster_text":{"text":text,"parts":parts},"provider":provider}
(dest/"server/internal/app/testdata/judge.json").write_text(json.dumps(assets,ensure_ascii=False,indent=2)+"\n")
print(f"Wrote {len(cases)} helpers/services, {len(queries)} queries and cluster-text response")
