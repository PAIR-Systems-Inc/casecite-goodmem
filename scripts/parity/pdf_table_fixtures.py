#!/usr/bin/env python3
"""Compare native PDF tables with the pinned Python reader (development only).

Requires reportlab, pdfplumber, pypdf. Generates deterministic real PDFs;
ordinary Go tests consume the checked-in JSON, without a Python runtime.
"""
import base64
import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
from types import ModuleType, SimpleNamespace
from reportlab.pdfgen import canvas
from pypdf import PdfReader, PdfWriter

BASELINE = '6605cb3474cf0c2894080af216d5fed2a81fc997'
root = Path(sys.argv[1]).resolve()
source = subprocess.check_output(['git', 'show', f'{BASELINE}:backend/app/services/text_extraction.py'], cwd=root)
config = ModuleType('app.config'); config.settings = SimpleNamespace()
sys.modules['app.config'] = config
module = ModuleType('upstream_pdf_tables'); sys.modules[module.__name__] = module
exec(compile(source, 'upstream_text_extraction.py', 'exec'), module.__dict__)
service = module.TextExtractionService()
output = Path('/tmp/casecite-pdf-table-fixtures'); output.mkdir(exist_ok=True)
rows = []
merged = PdfWriter()

def grid(c, x=50, y=600, width=500, height=90, rectangles=False, split=False):
    dx, dy = width/2, height/3
    if rectangles:
        for i in range(2):
            for j in range(3): c.rect(x+i*dx, y+j*dy, dx, dy, fill=0, stroke=1)
    else:
        for i in range(3):
            if split:
                c.line(x+i*dx, y, x+i*dx, y+height/2-1)
                c.line(x+i*dx, y+height/2+1, x+i*dx, y+height)
            else: c.line(x+i*dx, y, x+i*dx, y+height)
        for j in range(4): c.line(x,y+j*dy,x+width,y+j*dy)
    for i, row in enumerate([['Term','Value'],['Notice','30 days'],['Fee','$12,345.67']]):
        for j,text in enumerate(row): c.drawString(x+j*dx+8,y+height-(i+1)*dy+10,text)

def add(name, draw, *, ocr=False, reason=''):
    data = io.BytesIO(); c = canvas.Canvas(data, pagesize=(612,792), invariant=1, pageCompression=0)
    c.setFont('Helvetica', 10)
    c.drawString(50,750,'Agreement schedule: exact values must remain attached to the correct labels.')
    draw(c)
    c.drawString(50,70,'End of schedule. Confirm the original before relying on a quotation.')
    c.save(); raw=data.getvalue(); result=service.extract(file_content=raw,filename=name+'.pdf')
    if result.error: raise RuntimeError(result.error)
    rows.append(dict(name=name,content=base64.b64encode(raw).decode(),tables=result.tables,text=result.text,needs_ocr=ocr,reason=reason))
    (output/(name+'.pdf')).write_bytes(raw)
    merged.add_page(PdfReader(io.BytesIO(raw)).pages[0])

add('ruled', lambda c:grid(c))
add('rectangles', lambda c:grid(c,rectangles=True))
add('joined-rules', lambda c:grid(c,split=True))
def transformed(c):
    c.saveState(); c.translate(20,-20); c.scale(.85,1.03);grid(c);c.restoreState()
add('transformed',transformed)
def merged_header(c):
    c.rect(50,600,500,90)
    c.line(50,630,550,630);c.line(50,660,550,660);c.line(300,600,300,660)
    for x,y,text in [(58,670,'Payment and notice terms'),(58,640,'Notice'),(308,640,'30 days'),(58,610,'Fee'),(308,610,'$12,345.67')]:c.drawString(x,y,text)
add('merged-header',merged_header)
def merged_vertical(c):
    c.rect(50,600,500,90);c.line(300,600,300,690);c.line(50,660,550,660);c.line(300,630,550,630)
    for x,y,text in [(58,670,'Term'),(308,670,'Value'),(58,640,'Timing'),(308,640,'30 days'),(308,610,'60 days')]:c.drawString(x,y,text)
add('merged-vertical',merged_vertical)
def two(c):
    grid(c);c.drawString(50,560,'Between tables: the following schedule is a separate set of terms.');grid(c,y=400)
add('two-tables',two,reason='Keep the intervening paragraph and footer in physical reading order instead of moving them ahead of both tables.')
def literal(c):
    c.rect(50,600,500,60);c.line(300,600,300,660);c.line(50,630,550,630)
    for x,y,text in [(58,640,'Term'),(308,640,'Value'),(58,610,'Options'),(308,610,'A|B and C\\D')]:c.drawString(x,y,text)
add('literal-punctuation',literal,reason='Escape literal pipes and backslashes so a cell cannot invent Markdown columns.')
def plain(c):
    c.line(50,710,550,710);c.rect(50,600,500,80);c.drawString(60,640,'One bordered paragraph is not a multi-cell table.')
add('one-box',plain)
def form(c):
    c.beginForm('terms');grid(c);c.endForm();c.doForm('terms')
add('form-table',form,ocr=True,reason='GoodMem handles form XObjects; the native reader would otherwise accept only the surrounding header and omit the table.')
def rotated(c):
    c.saveState();c.translate(590,50);c.rotate(90);grid(c,x=50,y=50,width=500);c.restoreState()
add('rotated-table',rotated,ocr=True,reason='GoodMem handles rotated text that the native glyph reader cannot anchor reliably.')

def wrapped(c):
    c.rect(50,600,500,120);c.line(300,600,300,720)
    c.line(50,640,550,640);c.line(50,680,550,680)
    for x,y,text in [(58,695,'Term'),(308,695,'Value'),(58,660,'Notice'),(308,665,'30 days'),(308,650,'No penalty'),(58,620,'Optional')]:c.drawString(x,y,text)
add('wrapped-empty-cells',wrapped)

with (output/'all.pdf').open('wb') as f:merged.write(f)
destination=Path(__file__).resolve().parents[2]/'server/internal/app/testdata/pdf_tables.json'
destination.write_text(json.dumps(dict(baseline=BASELINE,source_sha256=hashlib.sha256(source).hexdigest(),cases=rows),ensure_ascii=False,indent=2)+'\n')
print(f'{len(rows)} PDF cases; rendered-source bundle: {output}/all.pdf')
