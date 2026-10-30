#!/usr/bin/env python3
"""Run pinned diagnostics orchestration and clear responses with controlled storage."""
import ast
import asyncio
import hashlib
import itertools
import json
import logging
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace as NS

BASELINE = '6605cb3474cf0c2894080af216d5fed2a81fc997'
root = Path(sys.argv[1]).resolve()
sources = {}

class Isolate(ast.NodeTransformer):
    def visit_ImportFrom(self, node): return None
    def visit_Import(self, node): return None

def function(path, name, env):
    raw = subprocess.check_output(['git', 'show', f'{BASELINE}:{path}'], cwd=root)
    sources[path] = hashlib.sha256(raw).hexdigest()
    fn = next(n for n in ast.parse(raw).body if isinstance(n, ast.AsyncFunctionDef) and n.name == name)
    fn.decorator_list = []
    fn.returns = None
    fn.args.defaults = []
    for arg in fn.args.args: arg.annotation = None
    fn = Isolate().visit(fn)
    exec(compile(ast.fix_missing_locations(ast.Module(body=[fn], type_ignores=[])), path, 'exec'), env)
    return env[name]

class Store:
    def _get_user_documents(self, user): return self.docs
    async def reindex_document(self, doc_id, user, keys):
        assert user == 'caller' and keys == 'stored-byok'
        outcome = self.outcomes[int(doc_id)]
        if outcome == 'missing': raise FileNotFoundError()
        if outcome == 'gone': return None
        if outcome == 'provider': raise RuntimeError('provider unavailable')
        return self.docs[doc_id]
    def _save_index(self): pass
    async def clear_all(self, user):
        assert user == 'caller'
        return self.clear_result

store = Store()
env = dict(document_service=store, UserAPIKeys=NS(for_user=lambda user: 'stored-byok'),
           embedding_service=NS(model='fixture-embedder'), DocumentStatus=NS(INDEXED='indexed'),
           logger=logging.getLogger('oracle'))
logging.disable(logging.CRITICAL)
reindex = function('backend/app/services/diagnostics.py', 'reindex_user_documents', env)
cases = []
for size in range(3):
    for outcomes in itertools.product(['indexed', 'failed', 'missing', 'gone', 'provider'], repeat=size):
        store.outcomes = outcomes
        store.docs = {str(i): NS(filename=f'file-{i}.pdf', status=outcome, metadata={'error': 'indexing failed'}) for i, outcome in enumerate(outcomes)}
        cases.append(dict(outcomes=outcomes, expected=asyncio.run(reindex('caller'))))

class HTTPError(Exception):
    def __init__(self, status_code=502, detail=''):
        if isinstance(status_code, str): detail, status_code = status_code, 502
        self.status, self.detail = status_code, detail

async def audit(**kwargs): pass
env.update(audit_service=NS(log_event=audit), AuditEventType=NS(SETTINGS_CHANGE='settings'),
           HTTPException=HTTPError, UpstreamError=HTTPError)
clear = function('backend/app/routers/settings.py', 'clear_index', env)
clears = []
for cleared, failed in itertools.product(range(5), repeat=2):
    store.clear_result = dict(cleared_documents=cleared, failed_documents=failed)
    case = dict(cleared=cleared, failed=failed)
    try: case.update(status=200, expected=asyncio.run(clear(NS(user_id='caller', email='caller@example.test'))))
    except HTTPError as e: case.update(status=e.status, expected={'detail': e.detail})
    clears.append(case)

dest = Path(__file__).resolve().parents[2] / 'server/internal/app/testdata/administration.json'
dest.write_text(json.dumps(dict(baseline=BASELINE, sources=sources, reindex=cases, clear=clears), indent=2)+'\n')
print(f'Wrote {len(cases)} reindex summaries and {len(clears)} clear-index responses')
