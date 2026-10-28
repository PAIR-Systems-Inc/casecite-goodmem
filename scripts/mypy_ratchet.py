#!/usr/bin/env python3
"""Blocking mypy with a per-package ratchet.

The codebase carries several hundred pre-existing mypy findings. Rather than
leaving mypy report-only until they are all gone, this gate makes it blocking
today on two rules:

1. No package may have MORE errors than its recorded baseline
   (``backend/mypy-baseline.json``). A package at 0 stays at 0, so every
   package that is clean is fully enforced, and no package can regress.
2. The baseline only ever goes down. When a run finds fewer errors than
   recorded, the gate passes and tells you to run ``--update`` so the
   lower number becomes the new ceiling.

Packages are the first two path components under ``backend/app``
(``routers``, ``services/rag``, ...); files directly under ``app`` count as
``(root)``.

Usage:
    python3 scripts/mypy_ratchet.py            # gate (exit 1 on regression)
    python3 scripts/mypy_ratchet.py --update   # rewrite the baseline downward
"""

from __future__ import annotations

import argparse
import collections
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
APP = ROOT / "backend" / "app"
BASELINE = ROOT / "backend" / "mypy-baseline.json"
MYPY_ARGS = ["mypy", "backend/app", "--ignore-missing-imports", "--no-error-summary"]
_ERROR = re.compile(r"^(?P<path>.*?\.py):\d+: error:")


def package_of(path: str) -> str:
    rel = Path(path.replace("\\", "/")).as_posix()
    rel = rel.split("backend/app/", 1)[-1]
    parts = rel.split("/")
    if len(parts) == 1:
        return "(root)"
    if len(parts) == 2:
        return parts[0]
    return parts[0] + "/" + parts[1]


def run_mypy() -> tuple[collections.Counter[str], list[str]]:
    proc = subprocess.run(  # noqa: S603 - fixed argv, no shell
        [sys.executable, "-m", *MYPY_ARGS], cwd=ROOT, capture_output=True, text=True
    )
    counts: collections.Counter[str] = collections.Counter()
    lines: list[str] = []
    for line in proc.stdout.splitlines():
        m = _ERROR.match(line)
        if m:
            counts[package_of(m.group("path"))] += 1
            lines.append(line)
    if proc.returncode not in (0, 1):  # 1 = type errors found; anything else is a crash
        sys.stderr.write(proc.stdout + proc.stderr)
        sys.exit(f"mypy exited with {proc.returncode}")
    return counts, lines


def load_baseline() -> dict[str, int]:
    if not BASELINE.exists():
        return {}
    return {k: int(v) for k, v in json.loads(BASELINE.read_text(encoding="utf-8")).items()}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--update", action="store_true", help="lower the baseline to the current counts"
    )
    args = parser.parse_args()

    counts, lines = run_mypy()
    baseline = load_baseline()
    packages = sorted(set(baseline) | set(counts))

    regressions: list[str] = []
    improvements: list[str] = []
    for pkg in packages:
        now, allowed = counts.get(pkg, 0), baseline.get(pkg, 0)
        if now > allowed:
            regressions.append(f"  {pkg}: {now} errors (baseline {allowed})")
        elif now < allowed:
            improvements.append(f"  {pkg}: {now} errors (baseline {allowed})")

    total, total_allowed = sum(counts.values()), sum(baseline.values())
    print(f"mypy: {total} errors across {len(counts)} packages (baseline {total_allowed})")

    if args.update:
        new = {pkg: counts.get(pkg, 0) for pkg in packages if counts.get(pkg, 0)}
        BASELINE.write_text(json.dumps(new, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(f"baseline written to {BASELINE.relative_to(ROOT)}")
        return 0

    if regressions:
        print("REGRESSION: packages with more mypy errors than their baseline:")
        print("\n".join(regressions))
        print("Offending lines (new errors are among these):")
        bad = {r.split(":")[0].strip() for r in regressions}
        for line in lines:
            m = _ERROR.match(line)
            if m and package_of(m.group("path")) in bad:
                print("  " + line)
        print("Fix the new errors, or if the count legitimately changed, re-run with --update.")
        return 1
    if improvements:
        print("Improved (run `python3 scripts/mypy_ratchet.py --update` to lock it in):")
        print("\n".join(improvements))
    else:
        print("OK: no package exceeds its baseline.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
