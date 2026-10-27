"""Deadline extraction reads the number a contract actually states.

"forty-five (45) days" was read as 5 days and "twenty-four (24) months" as 20
months: the word list lacked most tens and compound words kept only their first
word. The digits in parentheses are the drafter's own statement of the number.
"""

import os

os.environ.setdefault("SECRET_KEY", "test-secret-key-for-testing-only-32chars!")
os.environ.setdefault("ENCRYPTION_SALT", "test-salt-16chars!")
os.environ.setdefault("DEBUG", "true")

import pytest
from app.services.contract_analysis.deadlines import _word_to_int, extract_deadlines


def _offsets(text: str) -> list[int]:
    return [d.offset_days for d in extract_deadlines(text) if d.kind == "relative_offset"]


class TestWordToInt:
    @pytest.mark.parametrize(
        ("words", "expected"),
        [
            ("thirty", 30),
            ("forty-five", 45),
            ("twenty four", 24),
            ("Ninety-Six", 96),
            ("one hundred twenty", 120),
            ("one hundred and eighty", 180),
            ("fourteen", 14),
            ("30", 30),
        ],
    )
    def test_reads_compound_number_words(self, words, expected):
        assert _word_to_int(words) == expected

    def test_unknown_word_is_none(self):
        assert _word_to_int("several") is None


class TestOffsets:
    @pytest.mark.parametrize(
        ("clause", "days"),
        [
            ("Licensee shall pay within forty-five (45) days of the invoice date.", 45),
            ("Notice must be given within twenty-one (21) days after termination.", 21),
            ("The warranty runs for twenty-four (24) months following delivery.", 24 * 30),
            ("Either party may terminate on ninety (90) days' notice.", 90),
            ("Payment is due within 30 days of invoice.", 30),
            ("Cure within thirty days after notice.", 30),
            ("Records are kept for seven (7) years after expiration.", 7 * 365),
            ("No later than one hundred twenty (120) days after the Effective Date.", 120),
        ],
    )
    def test_amount_matches_the_contract(self, clause, days):
        assert _offsets(clause) == [days]

    def test_parenthesised_digits_win_over_the_words(self):
        # A typo in the words must not silently change the deadline.
        assert _offsets("within forty-five (54) days after notice") == [54]

    def test_before_is_negative(self):
        assert _offsets("at least ten (10) business days prior to the renewal date") == [-10]

    def test_period_amount(self):
        found = extract_deadlines("for a period of three (3) years thereafter")
        [period] = [d for d in found if d.kind == "period"]
        assert period.period_days == 3 * 365
