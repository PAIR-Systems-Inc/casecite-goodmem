"""Generate image-only and mixed PDF fixtures (development only).

Run with Pillow and ReportLab installed. PDFs use invariant metadata; no hidden
text layer is placed behind the scanned pages. The application has no dependency
on this script or a Python runtime.
"""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont
from reportlab.lib.utils import ImageReader
from reportlab.pdfgen.canvas import Canvas

OUT = Path(__file__).resolve().parents[2] / "server/internal/app/testdata"
OUT.mkdir(parents=True, exist_ok=True)
scan = Image.new("RGB", (1224, 1584), "white")
draw = ImageDraw.Draw(scan)
title = ImageFont.truetype("DejaVuSans.ttf", 36)
body = ImageFont.truetype("DejaVuSans.ttf", 32)
draw.text((120, 170), "SCANNED AGREEMENT", font=title, fill="black")
draw.text((120, 270), "Notice is 30 days.", font=body, fill="black")
draw.text((120, 330), "Payment is due monthly.", font=body, fill="black")
scan.save(OUT / "scanned_notice.png", optimize=True)

c = Canvas(str(OUT / "scanned_notice.pdf"), pagesize=(612, 792), invariant=1)
c.drawImage(ImageReader(scan), 0, 0, width=612, height=792)
c.showPage()
c.save()

c = Canvas(str(OUT / "mixed_notice.pdf"), pagesize=(612, 792), invariant=1)
c.setFont("Helvetica", 16)
c.drawString(60, 730, "Native first page. Payment is due monthly under this agreement.")
c.showPage()
c.drawImage(ImageReader(scan), 0, 0, width=612, height=792)
c.showPage()
c.drawImage(ImageReader(scan), 0, 0, width=612, height=792)
c.setFont("Helvetica", 16)
c.drawString(60, 760, "SEARCHABLE HEADER")
c.showPage()
c.save()
