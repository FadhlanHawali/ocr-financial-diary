"""The response shape returned by /ocr.

Kept identical to the original BCA-only server so sure-ocr-server and the
n8n workflow keep working; ``bank`` is an extra field they ignore.
"""
from __future__ import annotations

UNKNOWN = "Unknown"

# jenis_transaksi -> (flag field, detail field)
TRANSACTION_TYPES = {
    "Transfer VA": ("isVA", "transfer_va_detail"),
    "Transfer Domestik": ("isTransferDomestik", "transfer_domestik_detail"),
    "Transfer Antar Bank": ("isTransferAntarBank", "transfer_antarbank_detail"),
    "Transfer QRIS": ("isQris", "transfer_qris_detail"),
    "Transfer PLN": ("isPln", "transfer_pln_detail"),
}


def build_output(
    jenis_transaksi: str,
    from_account: str | None,
    detail: dict | None = None,
    bank: str | None = None,
) -> dict:
    output = {"jenis_transaksi": jenis_transaksi}
    for flag, _ in TRANSACTION_TYPES.values():
        output[flag] = False
    output["from_account"] = from_account
    for _, detail_field in TRANSACTION_TYPES.values():
        output[detail_field] = None

    if jenis_transaksi in TRANSACTION_TYPES:
        flag, detail_field = TRANSACTION_TYPES[jenis_transaksi]
        output[flag] = True
        output[detail_field] = detail
    output["transaction_date"] = None      # "YYYY-MM-DD", set by BankTemplate.parse
    output["transaction_datetime"] = None  # ISO 8601 with offset, e.g. "2026-01-15T12:30:45+07:00"
    output["bank"] = bank
    return output


def unknown_output(bank: str | None = None) -> dict:
    return build_output(UNKNOWN, None, None, bank)
