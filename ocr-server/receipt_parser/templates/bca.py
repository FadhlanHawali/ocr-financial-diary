"""BCA (m-BCA / myBCA / KlikBCA) receipts.

Layout: labels in the left column, values in the right column.
Amounts: English style, e.g. ``IDR 1,500,000.00``.
"""
from __future__ import annotations

from ..base import BankTemplate
from ..layouts import two_column_kv
from ..normalize import clean_account_number, digits_only, parse_amount
from ..output import UNKNOWN, build_output
from ..registry import register

KEYS = [
    "Nama Penerima", "Bank Tujuan", "No. Rekening Tujuan", "Rekening Tujuan",
    "Dari Rekening", "Nominal", "Biaya", "Layanan Transfer",
    "Berita", "Tujuan Transaksi", "Jenis Transaksi", "No. Referensi",
    "BCA Virtual Account", "Nama", "Nama Produk",
    "Total Tagihan", "Total Bayar", "billdesc",
    "Pembayaran ke", "Pengakuisisi", "RRN", "Lokasi Merchant",
    "Merchant PAN", "Terminal ID", "Sumber Dana", "Customer PAN",
    "Nominal Tujuan", "Mata Uang Tujuan", "Mata Uang Asal",
    "Jenis Produk PLN", "RP BAYAR",
]

AMOUNT_KEYS = {"Nominal", "Nominal Tujuan", "Total Tagihan", "Total Bayar", "Biaya", "billdesc"}

VA_KEYWORDS = ["BCA Virtual Account", "TOKOPEDIA", "OVO", "DANA", "GOPAY", "LINKAJA", "SHOPEEPAY"]


def _amount(text: str) -> int | None:
    return parse_amount(text, prefixes=("IDR",), thousands_sep=",", decimal_sep=".", require_decimals=True)


@register
class BCATemplate(BankTemplate):
    bank = "BCA"
    detect_keywords = ("BCA", "m-BCA", "myBCA", "KlikBCA")

    def extract(self, lines):
        raw = two_column_kv(lines, KEYS, split_ratio=0.45)
        for key in AMOUNT_KEYS & raw.keys():
            parsed = _amount(raw[key])
            if parsed is not None:
                raw[key] = parsed
        return raw

    def classify(self, raw: dict) -> str:
        jenis = raw.get("Jenis Transaksi", "")
        jenis_pln = raw.get("Jenis Produk PLN", "")
        nama_produk = raw.get("Nama Produk", "")

        if "QRIS" in jenis:
            return "Transfer QRIS"
        if any(kw.lower() in val.lower() for kw in VA_KEYWORDS for val in (jenis, nama_produk)):
            return "Transfer VA"
        if "Transfer" in jenis and "BCA" in jenis:
            return "Transfer Domestik"
        if "Transfer" in jenis:
            return "Transfer Antar Bank"
        if "PLN" in jenis_pln:
            return "Transfer PLN"
        return UNKNOWN

    def transform(self, raw):
        jenis_transaksi = self.classify(raw)
        from_account = clean_account_number(raw.get("Dari Rekening") or raw.get("Sumber Dana"))

        detail = None
        if jenis_transaksi == "Transfer VA":
            detail = {
                # Merchant/product (e.g. "SHOPEEPAY", "ASTRO"); "Nama" is the account holder
                "name": raw.get("Nama Produk") or raw.get("Nama"),
                "amount": raw.get("Total Bayar") or raw.get("billdesc") or raw.get("Total Tagihan"),
            }
        elif jenis_transaksi in ("Transfer Domestik", "Transfer Antar Bank"):
            detail = {
                "name": raw.get("Nama Penerima"),
                # Transfer amount, without the fee. BCA→BCA receipts label it "Nominal Tujuan"
                # (next to "Mata Uang Tujuan"), interbank receipts "Nominal".
                "amount": raw.get("Nominal") or raw.get("Nominal Tujuan"),
                "fee": raw.get("Biaya"),       # admin fee, e.g. 2500 for BI-FAST; None if not shown
                "description": raw.get("Berita"),
                # Destination, printed in full: lets the bot spot transfers to your own accounts.
                "to_account": digits_only(raw.get("No. Rekening Tujuan") or raw.get("Rekening Tujuan")),
                "to_bank": raw.get("Bank Tujuan") or ("BCA" if jenis_transaksi == "Transfer Domestik" else None),
            }
        elif jenis_transaksi == "Transfer QRIS":
            detail = {
                "merchant": raw.get("Pembayaran ke"),
                "amount": raw.get("Total Bayar") or raw.get("Nominal"),
                "rrn": raw.get("RRN"),
            }
        elif jenis_transaksi == "Transfer PLN":
            detail = {
                "name": raw.get("Jenis Produk PLN"),
                "amount": raw.get("RP BAYAR"),
            }

        return build_output(jenis_transaksi, from_account, detail)
