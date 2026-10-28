"""``{{step.field}}`` substitution for templated step parameters.

A deep-research question or drafting instruction may embed values from
earlier steps: ``{{extract.summary}}``, ``{{research.data.answer}}``,
``{{analysis.data.analyses.0.contract_type}}``. The lookup is a plain path
walk over the earlier step's JSON output — dictionary keys and list indexes
only. Nothing is evaluated; an unknown path renders as an empty string and is
reported, so the caller can show what was missing.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

from app.models.schemas import TEMPLATE_REF_RE

# A substituted value never exceeds this many characters, so one step's output
# cannot blow another step's prompt budget.
MAX_VALUE_CHARS = 12_000


@dataclass
class Rendered:
    text: str
    used: list[str] = field(default_factory=list)
    missing: list[str] = field(default_factory=list)


def _walk(value: Any, path: list[str]) -> tuple[bool, Any]:
    """Follow ``path`` through dicts and lists; (found, value)."""
    for part in path:
        if isinstance(value, dict):
            if part not in value:
                return False, None
            value = value[part]
        elif isinstance(value, list):
            if not part.isdigit() or int(part) >= len(value):
                return False, None
            value = value[int(part)]
        else:
            return False, None
    return True, value


def _as_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        text = value
    elif isinstance(value, bool):
        text = "yes" if value else "no"
    elif isinstance(value, int | float):
        text = str(value)
    else:
        text = json.dumps(value, ensure_ascii=False, indent=None, default=str)
    return text[:MAX_VALUE_CHARS]


def render_template(text: str, outputs: dict[str, dict[str, Any]]) -> Rendered:
    """Replace every ``{{step...}}`` in ``text`` from ``outputs`` (step name -> output).

    ``{{step}}`` alone renders the step's one-line ``summary``. Step names are
    matched case-insensitively, as the validator treats them.
    """
    lowered = {name.lower(): out for name, out in outputs.items()}
    rendered = Rendered(text="")

    def replace(match: Any) -> str:
        step = match.group(1)
        tail = [p for p in match.group(2).split(".") if p]
        ref = step + match.group(2)
        out = lowered.get(step.lower())
        if out is None:
            rendered.missing.append(ref)
            return ""
        found, value = _walk(out, tail or ["summary"])
        if not found:
            rendered.missing.append(ref)
            return ""
        rendered.used.append(ref)
        return _as_text(value)

    rendered.text = TEMPLATE_REF_RE.sub(replace, text)
    return rendered


def output_as_text(output: dict[str, Any], limit: int) -> str:
    """A step's output as reference text for a draft: the summary, then its data."""
    summary = str(output.get("summary") or "").strip()
    data = output.get("data")
    body = json.dumps(data, ensure_ascii=False, indent=1, default=str) if data else ""
    text = "\n".join(part for part in (summary, body) if part)
    return text[: max(0, limit)]
