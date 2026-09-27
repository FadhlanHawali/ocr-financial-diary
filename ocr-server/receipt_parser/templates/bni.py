"""BNI (wondr by BNI) receipts.

Layout (top to bottom):

    wondr / by BNI
    Transfer berhasil
    Rp100.000
    15 Jan 2026 · 12:30:45 WIB · Ref ID: ...
    Penerima            <- section: recipient name, then "BANK · account number"
    Sumber dana         <- section: sender name, then the masked account ("*******789")
    Detail transfer     <- section: label left, value right (Nominal, Biaya transaksi, ...)
    Total               Rp102.500

Amounts use the Indonesian style ``Rp100.000``. Only "Transfer berhasil" receipts are
supported so far (tested with an interbank transfer to BCA via BI-FAST).
"""
from __future__ import annotations

import re

from ..base import BankTemplate
from ..layouts import two_column_kv
from ..lines import OcrLine
from ..normalize import digits_only, parse_amount
from ..output import UNKNOWN, build_output
from ..registry import register

BANK_NAME = "BNI"

# Section headers, matched case-insensitively on a whole line.
SECTIONS = {
    "penerima": "Penerima",
    "sumber dana": "Sumber dana",
    "detail transfer": "Detail transfer",
}

DETAIL_KEYS = [
    "Nominal", "Biaya transaksi", "Metode transfer", "BIZ ID", "Tujuan transaksi",
    "Catatan", "Berita", "Keterangan", "Total",
]
AMOUNT_KEYS = {"Nominal", "Biaya transaksi", "Total"}

# The light "BNI" watermark can be read as separate text lines.
WATERMARK = re.compile(r"^[\W_]*BNI[\W_]*$", re.IGNORECASE)

# "BCA · 1234567890" (the dot may be read as ·, •, . or -)
BANK_ACCOUNT = re.compile(r"^(?P<bank>[A-Za-z][A-Za-z .&]*?)\s*[·•.\-]\s*(?P<account>[\d*][\d* ]*)$")


def _section_of(text: str) -> str | None:
    t = text.strip().lower()
    for key, name in SECTIONS.items():
        if t == key:
            return name
    return None


def _masked_account(text: str | None) -> str | None:
    """Keep digits and mask characters, drop spaces: "*******789" stays "*******789"."""
    if not text:
        return None
    compact = re.sub(r"\s+", "", text)
    m = re.search(r"[\d*][\d*\-]*[\d*]", compact)
    return m.group() if m else None


@register
class BNITemplate(BankTemplate):
    bank = "BNI"
    # "BNI" alone is not enough: BCA receipts for a transfer *to* BNI contain it too.
    detect_keywords = ("wondr", "by BNI", "BIZ ID", "Metode transfer", "Biaya transaksi")
    min_score = 2

    def extract(self, lines):
        lines = [l for l in lines if not WATERMARK.match(l.text)]
        raw: dict = {}

        # Header: title, and "Ref ID: ..."
        for l in lines:
            low = l.text.lower()
            if "berhasil" in low and "title" not in raw:
                raw["title"] = l.text
            if low.startswith("ref id"):
                raw["Ref ID"] = l.text.split(":", 1)[-1].strip()

        # Split the receipt into sections by their header lines.
        sections: dict[str, list[OcrLine]] = {}
        current = None
        for l in lines:
            name = _section_of(l.text)
            if name:
                current = name
                sections[current] = []
            elif current:
                sections[current].append(l)

        recipient = [l.text for l in sections.get("Penerima", [])]
        if recipient:
            raw["Nama Penerima"] = recipient[0]
        for text in recipient[1:]:
            m = BANK_ACCOUNT.match(text.strip())
            if m:
                raw["Bank Penerima"] = m.group("bank").strip()
                raw["Rekening Penerima"] = m.group("account").replace(" ", "")
                break

        source = [l.text for l in sections.get("Sumber dana", [])]
        if source:
            raw["Nama Pengirim"] = source[0]
        for text in source[1:]:
            acc = _masked_account(text)
            if acc:
                raw["Sumber Dana"] = acc
                break

        detail_lines = sections.get("Detail transfer", [])
        raw.update(two_column_kv(detail_lines, DETAIL_KEYS, split_ratio=0.45))
        for key in AMOUNT_KEYS & raw.keys():
            parsed = parse_amount(raw[key])
            if parsed is not None:
                raw[key] = parsed
        return raw

    def classify(self, raw: dict) -> str:
        if "transfer" not in raw.get("title", "").lower():
            return UNKNOWN
        bank = raw.get("Bank Penerima", "").upper()
        if not bank:
            return UNKNOWN
        return "Transfer Domestik" if bank == BANK_NAME else "Transfer Antar Bank"

    def transform(self, raw):
        jenis = self.classify(raw)
        detail = None
        if jenis in ("Transfer Domestik", "Transfer Antar Bank"):
            detail = {
                "name": raw.get("Nama Penerima"),
                "amount": raw.get("Nominal"),       # transfer amount, without the fee
                "fee": raw.get("Biaya transaksi"),  # admin fee; Total = amount + fee
                "description": raw.get("Catatan") or raw.get("Berita") or raw.get("Keterangan"),
                # Destination, printed in full: lets the bot spot transfers to your own accounts.
                "to_account": digits_only(raw.get("Rekening Penerima")),
                "to_bank": raw.get("Bank Penerima"),
            }
        return build_output(jenis, raw.get("Sumber Dana"), detail)
