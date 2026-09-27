"""Bank-agnostic receipt parsing.

Flow:  PaddleOCR result -> OcrLine list -> pick a BankTemplate -> template.parse()

To support a new bank, add a module under ``receipt_parser/templates/``
(see templates/README.md). Nothing in this package imports PaddleOCR, so it
can be unit-tested without the OCR model.
"""
from .base import BankTemplate
from .lines import OcrLine, lines_from_paddle
from .registry import available_banks, detect_template, get_template, parse_receipt, register

# Importing the templates package registers every bundled template.
from . import templates  # noqa: F401,E402

__all__ = [
    "BankTemplate",
    "OcrLine",
    "lines_from_paddle",
    "available_banks",
    "detect_template",
    "get_template",
    "parse_receipt",
    "register",
]
