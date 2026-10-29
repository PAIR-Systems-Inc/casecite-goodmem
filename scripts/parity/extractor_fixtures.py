#!/usr/bin/env python3
"""Generate golden results by executing the pinned, unmodified Python extractors.

Python is a development-only oracle, never an application runtime dependency.
Usage: python3 scripts/parity/extractor_fixtures.py ../casecite
"""
import ast
import dataclasses
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import types
from datetime import datetime

BASELINE = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
target = Path(__file__).resolve().parents[2] / "server/internal/app/testdata/contract_extractors.json"

for name in ("app", "app.services", "app.services.contract_analysis"):
    module = types.ModuleType(name)
    module.__path__ = []
    sys.modules[name] = module

modules, sources = {}, {}
for name in ("parties", "obligations", "deadlines", "defined_terms"):
    relative = f"backend/app/services/contract_analysis/{name}.py"
    path = root / relative
    pinned = subprocess.check_output(["git", "show", f"{BASELINE}:{relative}"], cwd=root)
    if path.read_bytes() != pinned:
        raise SystemExit(f"Refusing to use modified oracle: {relative}")
    sources[relative] = hashlib.sha256(pinned).hexdigest()
    full_name = f"app.services.contract_analysis.{name}"
    spec = importlib.util.spec_from_file_location(full_name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[full_name] = module
    spec.loader.exec_module(module)
    modules[name] = module

cases = {}
for filename in ("test_contract_extractors.py", "test_contract_deadlines.py"):
    tree = ast.parse((root / "backend/tests" / filename).read_text())
    for node in ast.walk(tree):
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            value = node.value
            if " " in value and 25 <= len(value) <= 500 and not value.startswith("\n"):
                cases.setdefault(f"{filename}:{node.lineno}", value)

cases.update({
    "empty": "",
    "aliases": 'Acme Corporation ("Provider") and Bravo LLC ("Customer"). Provider shall deliver the goods within ten days. Customer must pay invoices within forty-five (45) days after the Effective Date.',
    "repeated_alias": 'Acme Inc. ("Provider"). Acme Corporation ("Provider"). Provider agrees to maintain records throughout the term.',
    "recitals": "This Agreement is by and between Acme Corporation and Bravo LLC. Each party shall maintain confidentiality forever.",
    "defined_terms": '"Services" means the work described in Schedule A. Services include installation. "Unused Term" shall mean a thing never mentioned elsewhere. "Circular Term" means all Circular Term deliverables. Special Deliverables are required. Special Deliverables are accepted. Special Deliverables are paid.',
    "paragraph_quotes": 'Provider shall not disclose information.\n"Customer" must not copy it.\n(The parties agree to protect it.)',
    "unicode_offsets": 'Résumé 📜. Provider shall pay €500 within twenty-one (21) days after the Effective Date. "Services" means the café work. Services are due on January 2, 2027.',
    "periods_dates": "A term of twenty-four (24) months following execution. Notice is due at least ten (10) business days prior to termination. January 2, 2027. Feb 30, 2028. March 4, 2029.",
    "dedup_and_cap": " ".join(f"Provider shall deliver item number {i} to Customer promptly." for i in range(200)),
    "all_roles": "Provider Customer Client Licensor Licensee Buyer Seller Disclosing Party Receiving Party Discloser Recipient Indemnitor Indemnitee Company Employee Contractor Vendor Supplier Lessor Lessee Landlord Tenant",
    "conditions": "Customer shall promptly pay all invoices if accepted, upon delivery, subject to inspection, provided, however, that the goods comply. Provider is obligated to maintain insurance at all times. Both parties will protect confidential information in perpetuity.",
})

effective = datetime(2026, 1, 1)
fixtures = []
for label, text in cases.items():
    parties = modules["parties"].resolve_parties(text)
    obligations = modules["obligations"].extract_obligations(text, parties)
    deadlines = modules["deadlines"].extract_deadlines(text, effective)
    modules["obligations"].link_deadlines(obligations, deadlines)
    terms = modules["defined_terms"].check_defined_terms(text)
    expected = {key: [dataclasses.asdict(x) for x in rows] for key, rows in (
        ("parties", parties), ("obligations", obligations), ("deadlines", deadlines), ("defined_terms", terms)
    )}
    # The browser's String.slice uses UTF-16. Preserve upstream's matched text,
    # while correcting its code-point offsets for non-BMP characters explicitly.
    for rows in expected.values():
        for item in rows:
            for key in ("span_start", "span_end", "first_span_start", "first_span_end"):
                if isinstance(item.get(key), int):
                    item[key] = len(text[:item[key]].encode("utf-16-le")) // 2
            if isinstance(item.get("resolved_date"), datetime):
                item["resolved_date"] = item["resolved_date"].isoformat()
    fixtures.append({"name": label, "text": text, "effective_date": effective.isoformat(), "expected": expected})

target.parent.mkdir(parents=True, exist_ok=True)
target.write_text(json.dumps({"baseline": BASELINE, "source_sha256": sources,
    "offset_normalization": "Python code points to browser UTF-16; all other fields unchanged",
    "cases": fixtures}, ensure_ascii=False, indent=2) + "\n")
print(f"Wrote {len(fixtures)} fixtures to {target}")
