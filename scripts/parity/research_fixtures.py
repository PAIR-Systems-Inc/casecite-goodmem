#!/usr/bin/env python3
"""Extract pinned research prompts and execute deterministic research rules."""
import ast
import dataclasses
import hashlib
import html
import json
import math
from enum import Enum
from pathlib import Path
import re
import subprocess
import sys
import types
from typing import Any

baseline="6605cb3474cf0c2894080af216d5fed2a81fc997"
root=Path(sys.argv[1]).resolve()
dest=Path(__file__).resolve().parents[2]
sources={}
def load(path,names,extra=None):
    raw=subprocess.check_output(["git","show",f"{baseline}:{path}"],cwd=root)
    sources[path]=hashlib.sha256(raw).hexdigest()
    module=types.ModuleType("research_oracle_"+Path(path).stem);sys.modules[module.__name__]=module
    scope=module.__dict__;scope.update(dict(re=re,json=json,html=html,math=math,Enum=Enum,Any=Any,dataclass=dataclasses.dataclass,field=dataclasses.field));scope.update(extra or {})
    nodes=[]
    for node in ast.parse(raw).body:
        name=getattr(node,"name",None)
        if isinstance(node,ast.Assign):name=getattr(node.targets[0],"id",None)
        if isinstance(node,ast.AnnAssign):name=getattr(node.target,"id",None)
        if name in names:nodes.append(node)
    exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module="__future__",names=[ast.alias(name="annotations")],level=0),*nodes],type_ignores=[])),path,"exec"),scope)
    return scope

safety=load("backend/app/services/rag/prompt_safety.py",{"BEGIN_PREFIX","BEGIN_SUFFIX","END_MARKER","UNTRUSTED_CONTENT_RULE","_DELIMITER_RE","_NEUTRALISED","neutralise_delimiters","_clean_label","untrusted_block"})
planner=load("backend/app/services/deep_research/planner.py",{"TOOL_DOCUMENTS","TOOL_CASE_LAW","ALL_TOOLS","MIN_SUBQUESTIONS","_tool_lines","build_plan_prompt","normalise_plan"},safety)
passages=load("backend/app/services/deep_research/passages.py",{"REF_RE","_BRACKET_RE","SNIPPET_MAX_CHARS","extract_refs","join_refs_in_text"})
pipeline=load("backend/app/services/deep_research/pipeline.py",{"ANSWER_BUDGET_CHARS","SYNTHESIS_BUDGET_CHARS","build_answer_prompt","build_synthesis_prompt"},{**safety,"_passage_lines":lambda p,b:"__PASSAGES__"})
prompts={"plan":planner["build_plan_prompt"]("__QUESTION__",{"documents":True,"case_law":True},8),"answer":pipeline["build_answer_prompt"]("__QUESTION__",[],True),"answer_final":pipeline["build_answer_prompt"]("__QUESTION__",[],False),"synthesis":pipeline["build_synthesis_prompt"]("__QUESTION__",[types.SimpleNamespace(id="__STEP__",question="__SUBQUESTION__",answer="__ANSWER__")],[]),"untrusted_rule":safety["UNTRUSTED_CONTENT_RULE"]}
prompts["synthesis"]=prompts["synthesis"].replace("[__STEP__] __SUBQUESTION__\n__ANSWER__","__FINDINGS__")
(dest/"server/internal/app/research_prompts.json").write_text(json.dumps(prompts,ensure_ascii=False,indent=2)+"\n")

plans=[]
raws=[None,{}, {"sub_questions":[None,{}, {"question":"x"}, {"question":"  What applies? ","tools":["DOCUMENTS","shell","documents"]},{"question":"what applies?","tools":["case_law"]},{"question":"Other question","tools":["shell"]}]}, {"sub_questions":[{"question":f"Question {i}?","tools":["case_law","documents"]}for i in range(12)]}]
for raw in raws:
    for available in [{"documents":True,"case_law":True},{"documents":True,"case_law":False},{"documents":False,"case_law":False}]:
        plans.append(dict(raw=raw,available=available,cap=8,expected=planner["normalise_plan"](raw,available,8)))
refs=[]
for text in ["Supported [P1]. Missing [P99]. Mixed [P2, P9, P1].", "[P1, P1] [plain text] [P1, note] [p2]", "No references.", "[P99]  . [P2] ; 📜 [P3, P1]", "[P0] [P01] [P1]\n[P2]"]:
    clean,cited,stripped=passages["join_refs_in_text"](text,{"P1","P2"})
    refs.append(dict(text=text,allowed=["P1","P2"],expected=dict(text=clean,cited=cited,stripped=stripped)))
draft=load("backend/app/services/contract_analysis/long_drafting.py",{"_WORD_RE","_STOP","_stem","_term_list","_terms","_relevance"})
excerpt=load("backend/app/services/deep_research/tools.py",{"PASSAGE_MAX_CHARS","_PARA_SPLIT_RE","best_excerpt"},draft)
excerpts=[]
for text in ["Short opinion.","📜 Introduction.\n\n"+"Background facts. "*500+"\n\nAssignment requires consent.\n\n"+"More background. "*500,"No paragraph split "*600]:
    for query in ["assignment consent", "absent words", "the and"]:
        excerpts.append(dict(text=text,query=query,limit=4000,expected=excerpt["best_excerpt"](text,query,4000)))
base=type("ClientBase",(),{"set_token":lambda *a:None})
court=load("backend/app/services/courtlistener.py",{"_TAG_RE","_html_to_text","CitationTreatment","_NAME_STOPWORDS","_name_tokens","_normalize_citation","CourtListenerService"},{"CourtListenerClientBase":base,"html_lib":html,"settings":types.SimpleNamespace(courtlistener_api_token=None)})
service=court["CourtListenerService"]()
treatments=[]
for text in ["We follow this rule.","We followed Smith.","We overruled <mark>Smith</mark>.","Smith was overruled on other grounds.","Smith, rev'd in part, 500 U.S. 1.","The defendant's argument was rejected, citing Smith.","We reject the reasoning in Smith.","We distinguish Smith.","The question presented is one of limitations.","Smith was vacated in part on other grounds.","Smith was affirmed.","Smith was wrongly decided.","We called Smith into doubt.","The rule was superseded by statute.","We followed Smith until it was abrogated."]:
    treatments.append(dict(text=text,expected=service._analyze_treatment(text).value))
verdicts=[]
for negative in range(5):
    for caution in range(5):
        for read in (0,4):
            level,good=service._treatment_verdict(negative=negative,caution=caution,read=read)
            verdicts.append(dict(negative=negative,caution=caution,read=read,level=level,good=good))
fixture=dict(baseline=baseline,source_sha256=sources,plans=plans,refs=refs,excerpts=excerpts,treatments=treatments,verdicts=verdicts)
(dest/"server/internal/app/testdata/research.json").write_text(json.dumps(fixture,ensure_ascii=False,indent=2)+"\n")
print(f"Wrote {len(plans)} plans, {len(refs)} reference, {len(excerpts)} excerpt, {len(treatments)} treatment, {len(verdicts)} verdict cases and four original prompts")
