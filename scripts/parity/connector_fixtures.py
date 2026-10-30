#!/usr/bin/env python3
"""Execute pinned provider methods and input schemas without application startup."""
import ast
import asyncio
from dataclasses import asdict, dataclass
from datetime import UTC, datetime
from enum import Enum
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
from types import SimpleNamespace
from typing import Any
from urllib.parse import quote, urlencode, urlsplit, parse_qs
from pydantic import BaseModel, Field, ValidationError

BASELINE = '6605cb3474cf0c2894080af216d5fed2a81fc997'
root = Path(sys.argv[1]).resolve()
sources = {}
def source(path):
    raw = subprocess.check_output(['git','show',f'{BASELINE}:{path}'],cwd=root)
    sources[path] = hashlib.sha256(raw).hexdigest()
    return ast.parse(raw)

env = dict(Any=Any, Enum=Enum, BaseModel=BaseModel, Field=Field)
schema = source('backend/app/models/schemas.py')
names = {'ConnectorType','GooglePickerFile','OneDrivePickerFile','BoxPickerFile','DropboxChooserFile'}
exec(compile(ast.fix_missing_locations(ast.Module(body=[n for n in schema.body if isinstance(n,ast.ClassDef) and n.name in names],type_ignores=[])), 'schemas','exec'), env)
tree = source('backend/app/routers/connectors.py')
env['_FOLDER_ID_MAX_LENGTH']=1024
exec(compile(ast.fix_missing_locations(ast.Module(body=[n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='SyncRequest'],type_ignores=[])),'sync','exec'),env)
requests=[]
for provider,model,valid in [
 ('google','GooglePickerFile',dict(id='g',name='notice.pdf',mimeType='application/pdf',oauthToken='private-token')),
 ('microsoft','OneDrivePickerFile',dict(id='m',name='notice.pdf',accessToken='private-token')),
 ('box','BoxPickerFile',dict(id='b',name='notice.pdf',accessToken='private-token')),
 ('dropbox','DropboxChooserFile',dict(name='notice.pdf',link='https://dl.dropboxusercontent.com/test'))]:
    inputs=[valid,{},dict(valid,unknown='discarded')]
    for key in env[model].model_fields:
        inputs += [dict(valid,**{key:value}) for value in [None,'',4,True,[],{},'2048',2048.0]]
    for value in inputs:
        case=dict(kind='picker',provider=provider,input=value)
        try: case.update(valid=True,expected=env[model](**value).model_dump(mode='json'))
        except ValidationError: case['valid']=False
        requests.append(case)
for folder in [None,'','root','a'*1024,'a'*1025,5,False]:
    for sync in [False,True,'yes','off',None,2]:
        value=dict(folder_id=folder,sync_all=sync);case=dict(kind='sync',input=value)
        try: case.update(valid=True,expected=env['SyncRequest'](**value).model_dump(mode='json'))
        except ValidationError: case['valid']=False
        requests.append(case)

class FrozenDateTime(datetime):
    @classmethod
    def now(cls,tz=None): return cls(2026,10,29,12,0,0,tzinfo=UTC)
@dataclass
class FileInfo:
    id:str; name:str; mime_type:str; size:int; modified_at:datetime
    path:str|None=None; download_url:str|None=None; metadata:dict|None=None
class Base:
    def __init__(self): self.credentials={'access_token':'access','customer_id':'cust'}
class Response:
    def __init__(self,data): self.data=data
    def raise_for_status(self): pass
    def json(self): return self.data
class Client:
    payload={}
    async def __aenter__(self): return self
    async def __aexit__(self,*args): pass
    async def get(self,*args,**kwargs): return Response(self.payload)
    async def post(self,*args,**kwargs): return Response(self.payload)
async def noop(): pass
async def empty_drives(client): return []
values={}
for prefix in ['google','microsoft','box','clio','netdocuments','imanage']:
    values.update({prefix+'_client_id':'client-id',prefix+'_client_secret':'client-secret',prefix+'_redirect_uri':'https://casecite.test/callback'})
values.update(dropbox_app_key='client-id',dropbox_app_secret='client-secret',dropbox_redirect_uri='https://casecite.test/callback',microsoft_tenant_id='common',clio_base_url='https://eu.app.clio.com',netdocuments_vault_host='https://vault.netvoyage.com',netdocuments_api_host='https://api.vault.netvoyage.com',imanage_base_url='https://cloudimanage.com',filevine_base_url='https://api.filevine.io',filevine_enabled=True,filevine_api_key='filevine-key',filevine_api_secret='filevine-secret')
units=[]
modules={'google_drive':'GoogleDriveConnector','microsoft':'MicrosoftConnector','box':'BoxConnector','dropbox':'DropboxConnector','clio':'ClioConnector','filevine':'FilevineConnector','netdocuments':'NetDocumentsConnector','imanage':'IManageConnector'}
base=dict(id='abc',name='Notice.pdf',size=123,modifiedTime='2026-10-29T12:00:00Z',mimeType='application/pdf')
listings={
 'google_drive':[('',{'files':[base,dict(base,id='doc',name='Contract',mimeType='application/vnd.google-apps.document'),dict(base,id='sheet',name='Table',mimeType='application/vnd.google-apps.spreadsheet'),dict(base,id='slide',name='Slides',mimeType='application/vnd.google-apps.presentation')],'nextPageToken':'next'})],
 'microsoft':[('drv:drive:folder',{'value':[dict(base,file={'mimeType':'application/pdf'},lastModifiedDateTime='2026-10-29T12:00:00Z',parentReference={'path':'/Cases'},webUrl='https://example.test/file'),dict(base,id='folder',folder={},lastModifiedDateTime='2026-10-29T12:00:00Z')],'@odata.nextLink':'https://graph.microsoft.com/v1.0/next'})],
 'box':[('0',{'entries':[dict(base,type='file',modified_at='2026-10-29T12:00:00Z'),dict(base,id='folder',type='folder')],'next_marker':'next'})],
 'dropbox':[('',{'entries':[dict(base,**{'.tag':'file','path_lower':'/notice.pdf','path_display':'/Notice.pdf','server_modified':'2026-10-29T12:00:00Z'}),dict(base,id='folder',**{'.tag':'folder','path_lower':'/folder'})],'cursor':'next','has_more':True})],
 'clio':[('',{'data':[dict(base,content_type='application/pdf',updated_at='2026-10-29T12:00:00Z',matter={'id':123,'display_number':'Matter 123'})],'meta':{'paging':{'next':'https://eu.app.clio.com/api/v4/documents.json?page=2'}}})],
 'filevine':[('',{'items':[{'projectId':{'native':123},'projectName':'Matter 123'}],'hasMore':True}),('123',{'items':[{'docId':{'native':456},'filename':'Notice','extension':'pdf','size':123,'lastUpdated':'2026-10-29T12:00:00Z'}],'hasMore':True})],
 'netdocuments':[('',{'cabinets':[{'id':'c1','name':'Cabinet'}]}),('cabinet:c1',{'list':[{'id':'f1','name':'Folder'}]}),('f1',{'list':[dict(base,type='document',modified={'date':'2026-10-29T12:00:00Z'},cabinet='c1',version=2,extension='pdf')],'total':101})],
 'imanage':[('',{'data':[dict(base,id='LIB!123',extension='pdf',edit_date='2026-10-29T12:00:00Z',database='LIB',version=2,**{'class':'CONTRACT','author':'Person'})],'total_count':101})]
}
for module,classname in modules.items():
    tree=source(f'backend/app/services/connectors/{module}.py')
    nodes=[n for n in tree.body if isinstance(n,ast.ClassDef) or isinstance(n,(ast.FunctionDef,ast.Assign,ast.AnnAssign)) and (getattr(n,'name','') in {'_seg','_native'} or isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id in {'GRAPH','_SESSION_TTL_SECONDS'} for t in n.targets) or isinstance(n,ast.AnnAssign) and n.target.id=='GOOGLE_EXPORT_TYPES')]
    g=dict(Any=Any,FileInfo=FileInfo,BaseConnector=Base,ConnectorError=ValueError,ConnectorType=env['ConnectorType'],datetime=FrozenDateTime,UTC=UTC,quote=quote,urlencode=urlencode,settings=SimpleNamespace(**values),connector_credentials=SimpleNamespace(get=lambda k:values.get(k)),httpx=SimpleNamespace(AsyncClient=Client,HTTPError=Exception,HTTPStatusError=Exception,RequestError=Exception),json=json,EXTENSION_TO_MIME={'pdf':'application/pdf'})
    exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),module,'exec'),g)
    connector=g[classname]();connector._ensure_valid_token=noop;connector._ensure_session=noop
    if module=='microsoft': connector._list_site_drives=empty_drives
    provider='onedrive' if module=='microsoft' else module
    if module!='filevine':
        for state in ['state123','state &/📜']:
            raw=connector.get_auth_url(state);parts=urlsplit(raw)
            units.append(dict(kind='auth_url',provider=provider,values=values,state=state,expected=dict(base=f'{parts.scheme}://{parts.netloc}{parts.path}',query=parse_qs(parts.query))))
    for folder,payload in listings[module]:
        Client.payload=payload
        files,next_token=asyncio.run(connector.list_files(folder or None,None))
        expected=[]
        for f in files:
            d=asdict(f);d['modified_at']=f.modified_at.astimezone(UTC).isoformat().replace('+00:00','Z');d['path']=d['path'] or '';d.pop('download_url',None)
            # Source methods invent "now" when no revision date exists; the Go
            # port deliberately leaves that value unknown. Folders have none.
            if f.mime_type=='folder': d['modified_at']=''
            expected.append(d)
        units.append(dict(kind='listing',provider=provider,folder=folder,payload=payload,expected=expected,next=next_token))

destination=Path(__file__).resolve().parents[2]/'server/internal/app/testdata/connectors.json'
destination.write_text(json.dumps(dict(baseline=BASELINE,sources=sources,requests=requests,units=units),indent=2)+'\n')
print(f'Wrote {len(requests)} connector/picker schemas and {len(units)} provider comparisons')
