"""The user's review instructions, bounded for a prompt.

Every review, issue assessment and redline prompt carries the same standing
guidance: the user's practice profile, the firm playbook, and the message the
user typed for this run — in that order, so the message comes last.
"""

# The API bounds the profile and the playbook at 20,000 characters each; this
# leaves room for both plus the message, so a valid set is never cut.
INSTRUCTIONS_MAX_CHARS = 48_000


def bounded_instructions(text: str | None, limit: int = INSTRUCTIONS_MAX_CHARS) -> str:
    """The instructions whole whenever they fit.

    When they do not, the END is kept: the user's own message for this run
    comes last, after the standing profile and playbook, and it is the part
    that must never be lost ("I represent the vendor" changes every answer).
    """
    text = (text or "").strip()
    if len(text) <= limit:
        return text
    return "…" + text[-limit:]
