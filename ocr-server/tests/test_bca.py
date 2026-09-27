import unittest

from receipt_parser import available_banks, parse_receipt
from receipt_parser.normalize import find_datetime, parse_amount, parse_datetime

from .helpers import paddle_result, two_column


class TestBCA(unittest.TestCase):
    def parse(self, result, **kw):
        return parse_receipt(result, default_bank="BCA", **kw)["result"]

    def test_registered(self):
        self.assertIn("BCA", available_banks())

    def test_transfer_domestik(self):
        r = self.parse(two_column([
            ("Jenis Transaksi", "Transfer ke BCA"),
            ("Dari Rekening", "123 456 7890"),
            ("Rekening Tujuan", "111 - 222 - 3333"),
            ("Nama Penerima", ["JOHN", "DOE"]),
            ("Nominal", "IDR 1,500,000.00"),
            ("Berita", "bayar kos"),
        ]))
        self.assertEqual(r["jenis_transaksi"], "Transfer Domestik")
        self.assertTrue(r["isTransferDomestik"])
        self.assertEqual(r["bank"], "BCA")
        self.assertEqual(r["from_account"], "1234567890")
        self.assertEqual(r["transfer_domestik_detail"],
                         {"name": "JOHN DOE", "amount": 1500000, "fee": None, "description": "bayar kos",
                          "to_account": "1112223333", "to_bank": "BCA"})

    def test_transfer_domestik_nominal_tujuan(self):
        # BCA->BCA receipts label the amount "Nominal Tujuan" instead of "Nominal".
        r = self.parse(two_column([
            ("Nama Penerima", "JOHN DOE"),
            ("Rekening Tujuan", "123 - 456 - 7890"),
            ("Jenis Transaksi", ["Transfer ke Rekening", "BCA"]),
            ("Mata Uang Tujuan", ["IDR - Indonesian", "Rupiah"]),
            ("Dari Rekening", "987 - 6** - **21"),
            ("Mata Uang Asal", ["IDR - Indonesian", "Rupiah"]),
            ("Nominal Tujuan", "IDR 10,000,000.00"),
            ("Berita", "top up"),
        ]))
        self.assertEqual(r["jenis_transaksi"], "Transfer Domestik")
        self.assertEqual(r["transfer_domestik_detail"]["amount"], 10000000)
        self.assertEqual(r["transfer_domestik_detail"]["to_account"], "1234567890")
        self.assertEqual(r["from_account"], "987-6**-**21")

    def test_transfer_antar_bank(self):
        r = self.parse(two_column([
            ("Jenis Transaksi", "Transfer Online"),
            ("Dari Rekening", "1234567890"),
            ("Bank Tujuan", "MANDIRI"),
            ("No. Rekening Tujuan", ["555 - 1234 - 5678 -", "9012"]),
            ("Nama Penerima", "JOHN DOE"),
            ("Nominal", "IDR 250,000.00"),
            ("Biaya", "IDR 2,500.00"),
        ]))
        self.assertEqual(r["jenis_transaksi"], "Transfer Antar Bank")
        self.assertEqual(r["transfer_antarbank_detail"]["amount"], 250000)
        self.assertEqual(r["transfer_antarbank_detail"]["fee"], 2500)
        self.assertEqual(r["transfer_antarbank_detail"]["to_account"], "555123456789012")
        self.assertEqual(r["transfer_antarbank_detail"]["to_bank"], "MANDIRI")

    def test_qris(self):
        r = self.parse(two_column([
            ("Jenis Transaksi", "Pembayaran QRIS"),
            ("Sumber Dana", "1234567890"),
            ("Pembayaran ke", "WARUNG MAKAN"),
            ("Total Bayar", "IDR 35,000.00"),
            ("RRN", "000123456789"),
        ]))
        self.assertTrue(r["isQris"])
        self.assertEqual(r["transfer_qris_detail"],
                         {"merchant": "WARUNG MAKAN", "amount": 35000, "rrn": "000123456789"})

    def test_va_detected_from_nama_produk(self):
        # "Nama Produk" must not be swallowed by the shorter "Nama" key.
        r = self.parse(two_column([
            ("Nama Produk", "SHOPEEPAY"),
            ("Dari Rekening", "1234567890"),
            ("Nama", "JOHN DOE"),
            ("Total Bayar", "IDR 100,000.00"),
        ]))
        self.assertEqual(r["jenis_transaksi"], "Transfer VA")
        self.assertEqual(r["transfer_va_detail"], {"name": "SHOPEEPAY", "amount": 100000})

    def test_pln(self):
        r = self.parse(two_column([
            ("Jenis Produk PLN", "PLN PREPAID"),
            ("Dari Rekening", "1234567890"),
            ("RP BAYAR", "Rp 202.500"),
        ]))
        self.assertTrue(r["isPln"])
        self.assertEqual(r["transfer_pln_detail"]["name"], "PLN PREPAID")

    def test_unparsed_amount_kept_as_text(self):
        r = self.parse(two_column([
            ("Jenis Transaksi", "Transfer ke BCA"),
            ("Dari Rekening", "1234567890"),
            ("Nominal", "IDR 1,500,000"),
        ]))
        self.assertEqual(r["transfer_domestik_detail"]["amount"], "IDR 1,500,000")

    def test_default_bank_fallback_and_unknown(self):
        # No "BCA" text anywhere, so no template scores.
        rows = two_column([("Jenis Transaksi", "Transfer Online")], header=("Bukti Transfer",))
        self.assertEqual(parse_receipt(rows, default_bank="BCA")["result"]["bank"], "BCA")
        self.assertEqual(parse_receipt(rows, default_bank=None)["result"]["jenis_transaksi"], "Unknown")

    def test_bank_hint(self):
        r = parse_receipt(paddle_result([("anything", 10, 10)]), bank_hint="bca")["result"]
        self.assertEqual(r["bank"], "BCA")
        with self.assertRaises(KeyError):
            parse_receipt(paddle_result([("anything", 10, 10)]), bank_hint="NOPE")

    def test_empty(self):
        r = self.parse([])
        self.assertEqual(r["jenis_transaksi"], "Unknown")
        self.assertIsNone(r["transaction_date"])

    def test_transaction_date_from_header(self):
        r = self.parse(two_column(
            [("Jenis Transaksi", "Transfer ke BCA"), ("Dari Rekening", "1234567890"),
             ("Berita", "arisan 1 Maret 2026")],
            header=("m-BCA", "Transfer Berhasil", "15 Jan 2026 12:30:45", "IDR 2,500,000.00"),
        ))
        self.assertEqual(r["transaction_date"], "2026-01-15")
        self.assertEqual(r["transaction_datetime"], "2026-01-15T12:30:45+07:00")

    def test_no_date_on_receipt(self):
        r = self.parse(two_column([("Jenis Transaksi", "Transfer ke BCA")]))
        self.assertIsNone(r["transaction_date"])
        self.assertIsNone(r["transaction_datetime"])


class TestParseDatetime(unittest.TestCase):
    def test_bca_header(self):
        dt = parse_datetime("20 Feb 2026 21:10:05")
        self.assertEqual(dt.isoformat(), "2026-02-20T21:10:05+07:00")

    def test_indonesian_months_and_separators(self):
        self.assertEqual(parse_datetime("5 Mei 2026, 08.30").isoformat(), "2026-05-05T08:30:00+07:00")
        self.assertEqual(parse_datetime("17 Agustus 2026").date().isoformat(), "2026-08-17")
        self.assertEqual(parse_datetime("01 Okt 2026 10:00").month, 10)
        self.assertEqual(parse_datetime("24 Des 2026 10:00").month, 12)

    def test_ocr_merged_year_and_time(self):
        self.assertEqual(parse_datetime("15 Jan 202612:30:45").isoformat(), "2026-01-15T12:30:45+07:00")

    def test_rejects_non_dates(self):
        self.assertIsNone(parse_datetime("maret 2026"))
        self.assertIsNone(parse_datetime("20260115ABCDEFGH"))
        self.assertIsNone(parse_datetime("31 Feb 2026 10:00"))
        self.assertIsNone(parse_datetime("12 Foo 2026"))

    def test_find_prefers_line_with_time(self):
        dt = find_datetime(["arisan 1 Maret 2026", "15 Jan 2026 12:30:45"])
        self.assertEqual(dt.day, 15)
        self.assertEqual(find_datetime(["arisan 1 Maret 2026"]).day, 1)
        self.assertIsNone(find_datetime(["no date"]))


class TestParseAmount(unittest.TestCase):
    def test_indonesian_style(self):
        self.assertEqual(parse_amount("Rp 1.500.000,00"), 1500000)
        self.assertEqual(parse_amount("Rp1.500.000"), 1500000)
        self.assertEqual(parse_amount("Total IDR 25.000"), 25000)

    def test_english_style(self):
        kw = dict(prefixes=("IDR",), thousands_sep=",", decimal_sep=".", require_decimals=True)
        self.assertEqual(parse_amount("IDR 1,500,000.00", **kw), 1500000)
        self.assertIsNone(parse_amount("IDR 1,500,000", **kw))

    def test_none(self):
        self.assertIsNone(parse_amount(None))
        self.assertIsNone(parse_amount("no money here"))


if __name__ == "__main__":
    unittest.main()
