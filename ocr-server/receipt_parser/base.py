from __future__ import annotations

from abc import ABC, abstractmethod
from datetime import datetime

from .lines import OcrLine, full_text
from .normalize import find_datetime


class BankTemplate(ABC):
    """Base class for one bank / app receipt format.

    A template answers three questions:
      1. detect()    - does this receipt look like mine?  (higher score = more likely)
      2. extract()   - which raw label -> value pairs are on it?
      3. transform() - turn those raw fields into the /ocr response.
    """

    #: Short unique id, also accepted as ``/ocr?bank=<id>`` (case-insensitive).
    bank: str = ""
    #: Text that identifies this bank on a receipt. Each distinct hit adds 1 to the score.
    detect_keywords: tuple[str, ...] = ()
    #: Minimum score before this template is chosen automatically.
    min_score: int = 1
    #: UTC offset of the times printed on receipts (WIB = +7).
    tz_offset_hours: int = 7

    def detect(self, lines: list[OcrLine]) -> int:
        text = full_text(lines).lower()
        return sum(1 for kw in self.detect_keywords if kw.lower() in text)

    @abstractmethod
    def extract(self, lines: list[OcrLine]) -> dict:
        """Return raw fields, e.g. {"Nominal": 150000, "Nama Penerima": "JOHN DOE"}."""

    @abstractmethod
    def transform(self, raw: dict) -> dict:
        """Map raw fields to the response (use receipt_parser.output.build_output)."""

    def extract_datetime(self, lines: list[OcrLine], raw: dict) -> datetime | None:
        """When the transaction happened. Default: first "DD Mon YYYY HH:MM" on the receipt.

        Override if a bank prints the date under a label or in another format.
        """
        return find_datetime((l.text for l in lines), self.tz_offset_hours)

    def parse(self, lines: list[OcrLine]) -> tuple[dict, dict]:
        raw = self.extract(lines)
        result = self.transform(raw)
        dt = self.extract_datetime(lines, raw)
        if dt is not None:
            result["transaction_date"] = dt.date().isoformat()
            result["transaction_datetime"] = dt.isoformat()
        result["bank"] = self.bank
        return result, raw
