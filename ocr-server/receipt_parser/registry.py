from __future__ import annotations

import logging

from .base import BankTemplate
from .lines import OcrLine, lines_from_paddle
from .output import unknown_output

logger = logging.getLogger(__name__)

_TEMPLATES: dict[str, BankTemplate] = {}


def register(cls: type[BankTemplate]) -> type[BankTemplate]:
    """Class decorator: ``@register`` makes a template available for detection."""
    instance = cls()
    key = instance.bank.upper()
    if not key:
        raise ValueError(f"{cls.__name__} must set `bank`")
    if key in _TEMPLATES:
        raise ValueError(f"Duplicate bank template: {instance.bank}")
    _TEMPLATES[key] = instance
    return cls


def available_banks() -> list[str]:
    return sorted(t.bank for t in _TEMPLATES.values())


def get_template(bank: str | None) -> BankTemplate | None:
    return _TEMPLATES.get(bank.upper()) if bank else None


def detect_template(lines: list[OcrLine], default_bank: str | None = None) -> BankTemplate | None:
    """Pick the template with the highest score; fall back to ``default_bank``."""
    best, best_score = None, 0
    for template in _TEMPLATES.values():
        score = template.detect(lines)
        logger.debug("template %s scored %s", template.bank, score)
        if score >= template.min_score and score > best_score:
            best, best_score = template, score
    if best is None and default_bank:
        best = get_template(default_bank)
    return best


def parse_receipt(ocr_result, bank_hint: str | None = None, default_bank: str | None = None) -> dict:
    """Full pipeline. Returns {"result": <response>, "raw": <raw fields>, "lines": [...]}.

    ``bank_hint`` forces a template; unknown hints raise KeyError.
    """
    lines = lines_from_paddle(ocr_result)
    if bank_hint:
        template = get_template(bank_hint)
        if template is None:
            raise KeyError(bank_hint)
    else:
        template = detect_template(lines, default_bank)

    if template is None or not lines:
        return {"result": unknown_output(), "raw": {}, "lines": lines}

    result, raw = template.parse(lines)
    return {"result": result, "raw": raw, "lines": lines}
