"""Helpers for turning raw OCR strings into clean values."""
from __future__ import annotations

import re
from datetime import datetime, timedelta, timezone
from decimal import Decimal, InvalidOperation


def parse_amount(
    text: str | None,
    prefixes: tuple[str, ...] = ("IDR", "Rp"),
    thousands_sep: str = ".",
    decimal_sep: str = ",",
    require_decimals: bool = False,
) -> int | None:
    """Parse a money string into whole Rupiah.

    Defaults match the common Indonesian style ``Rp 1.500.000,00``.
    BCA uses the English style ``IDR 1,500,000.00`` -> pass
    ``prefixes=("IDR",), thousands_sep=",", decimal_sep=".", require_decimals=True``.
    Decimals are truncated. Returns None when no amount is found.
    """
    if not text:
        return None
    prefix = "|".join(re.escape(p) for p in prefixes)
    ts, ds = re.escape(thousands_sep), re.escape(decimal_sep)
    decimals = rf"{ds}\d{{2}}" if require_decimals else rf"(?:{ds}\d{{1,2}})?"
    match = re.search(rf"(?:{prefix})\.?\s*(\d[\d{ts}]*{decimals})", text, re.IGNORECASE)
    if not match:
        return None
    number = match.group(1).replace(thousands_sep, "").replace(decimal_sep, ".")
    try:
        return int(Decimal(number))
    except InvalidOperation:
        return None


def digits_only(text: str | None) -> str | None:
    """Full (unmasked) account number: "555 - 1234 - 5678 - 9012" -> "555123456789012"."""
    if not text:
        return None
    digits = re.sub(r"\D", "", text)
    return digits or None


def clean_account_number(text: str | None) -> str | None:
    """Keep the span from the first to the last digit and drop whitespace."""
    if not text:
        return text
    match = re.search(r"(\d[\s\S]*\d)", text)
    if match:
        return re.sub(r"\s+", "", match.group())
    return re.sub(r"\s+", "", text)


# English and Indonesian month names, keyed by their first three letters.
_MONTHS = {
    "jan": 1, "feb": 2, "peb": 2, "mar": 3, "apr": 4, "may": 5, "mei": 5,
    "jun": 6, "jul": 7, "aug": 8, "agu": 8, "agt": 8, "sep": 9,
    "oct": 10, "okt": 10, "nov": 11, "nop": 11, "dec": 12, "des": 12,
}

# "15 Jan 2026 12:30:45", "7 Maret 2026, 11.01", "15 Jan 2026 · 12:30:45 WIB" (BNI),
# "15 Jan 202612:30:45" (OCR merged)
_DATE_RE = re.compile(
    r"(?<!\d)(\d{1,2})\s*([A-Za-z]{3,9})\.?\s*(\d{4})"
    r"(?:\s*[,·•]?\s*(\d{1,2})[:.](\d{2})(?:[:.](\d{2}))?)?"
)


def parse_datetime(text: str | None, tz_offset_hours: int = 7) -> datetime | None:
    """Parse "DD Mon YYYY [HH:MM[:SS]]" (English or Indonesian month) into an aware datetime.

    Receipts from Indonesian banks are in WIB, so the default offset is +07:00.
    Returns None when no valid date is found.
    """
    if not text:
        return None
    tz = timezone(timedelta(hours=tz_offset_hours))
    for m in _DATE_RE.finditer(text):
        day, month_name, year, hh, mm, ss = m.groups()
        month = _MONTHS.get(month_name[:3].lower())
        if month is None:
            continue
        try:
            return datetime(int(year), month, int(day), int(hh or 0), int(mm or 0), int(ss or 0), tzinfo=tz)
        except ValueError:  # e.g. 31 Feb or an OCR misread
            continue
    return None


def find_datetime(texts, tz_offset_hours: int = 7) -> datetime | None:
    """Return the first date in ``texts`` (top to bottom), preferring one that has a time.

    Receipts print the transaction timestamp near the top; dates without a time
    (e.g. inside a note) are only used as a fallback.
    """
    fallback = None
    for text in texts:
        dt = parse_datetime(text, tz_offset_hours)
        if dt is None:
            continue
        if _DATE_RE.search(text).group(4) is not None:
            return dt
        fallback = fallback or dt
    return fallback
