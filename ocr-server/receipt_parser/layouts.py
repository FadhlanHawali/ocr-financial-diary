"""Generic strategies for turning positioned OCR lines into {label: raw value}.

Templates pick the layout that matches how their bank prints receipts.
"""
from __future__ import annotations

from .lines import OcrLine


def _match_key(text: str, keys: list[str]) -> str | None:
    """Return the longest known key contained in ``text`` (case-insensitive).

    Longest-first means "Nama Produk" wins over "Nama", and
    "Nominal Tujuan" over "Nominal".
    """
    lowered = text.lower()
    for key in sorted(keys, key=len, reverse=True):
        if key.lower() in lowered:
            return key
    return None


def two_column_kv(
    lines: list[OcrLine],
    keys: list[str],
    split_ratio: float = 0.45,
    key_y_buffer: float = 10,
    next_key_y_buffer: float = 5,
) -> dict[str, str]:
    """Labels in a left column, values in a right column (e.g. BCA).

    Every right-column line between one label and the next is joined into
    that label's value, so multi-line values (long names, notes) are kept.
    """
    if not lines:
        return {}
    width = max(l.x for l in lines)
    mid_x = width * split_ratio

    key_items = []
    for l in lines:
        if l.x < mid_x:
            key = _match_key(l.text, keys)
            if key:
                key_items.append((key, l.y))
    key_items.sort(key=lambda k: k[1])

    right_col = sorted((l for l in lines if l.x >= mid_x), key=lambda l: l.y)

    result: dict[str, str] = {}
    for i, (key, y) in enumerate(key_items):
        y_start = y - key_y_buffer
        y_end = key_items[i + 1][1] - next_key_y_buffer if i + 1 < len(key_items) else float("inf")
        values = [v.text for v in right_col if y_start <= v.y < y_end]
        if values:
            result[key] = " ".join(values)
    return result


def stacked_kv(lines: list[OcrLine], keys: list[str]) -> dict[str, str]:
    """Label on one line, value on the line(s) below it (common in BRImo, BNI, e-wallets).

    Everything between a label and the next label becomes that label's value.
    If a label line also contains a value after the label ("Biaya Rp0"), that
    remainder is used too.
    """
    result: dict[str, str] = {}
    current_key: str | None = None
    buffer: list[str] = []

    def flush():
        if current_key and buffer:
            result[current_key] = " ".join(buffer)

    for l in lines:
        key = _match_key(l.text, keys)
        if key:
            flush()
            current_key, buffer = key, []
            idx = l.text.lower().find(key.lower())
            rest = l.text[idx + len(key):].strip(" :")
            if rest:
                buffer.append(rest)
        elif current_key:
            buffer.append(l.text)
    flush()
    return result
