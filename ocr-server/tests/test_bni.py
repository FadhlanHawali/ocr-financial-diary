"""BNI (wondr) template, using a synthetic receipt laid out like the real app (fake names/numbers)."""
import unittest

from receipt_parser import parse_receipt
from receipt_parser.lines import lines_from_paddle

from .helpers import paddle_result


def wondr_transfer(recipient_bank="BCA", note=None):
    rows = [
        ("wondr", 219, 59), ("by BNI", 276, 86),
        ("Transfer berhasil", 220, 132), ("Rp250.000", 220, 182),
        ("15 Jan 2026· 12:30:45 WIB·", 218, 230),
        ("Ref ID: 20260115123045000001", 220, 252),
        ("Penerima", 63, 292), ("JOHN DOE", 94, 329), (f"{recipient_bank}·1234567890", 93, 356),
        ("Sumber dana", 76, 396), ("JOHN DOE", 94, 433), ("*******123", 64, 459),
        ("Detail transfer", 82, 500),
        ("Nominal", 56, 542), ("Rp250.000", 374, 542),
        ("Biaya transaksi", 78, 578), ("Rp2.500", 381, 578),
        ("Metode transfer", 84, 614), ("BI-FAST", 385, 614),
        ("BIZ ID", 48, 651), ("20260115BNINIDJA0100", 323, 651), ("0000000001", 374, 674),
        ("Tujuan transaksi", 84, 711), ("Lainnya", 387, 712),
    ]
    y = 748
    if note:
        rows += [("Catatan", 50, y), (note, 380, y)]
        y += 37
    rows += [("Total", 44, y + 20), ("Rp252.500", 344, y + 22)]
    return paddle_result(rows)


class TestBNI(unittest.TestCase):
    def parse(self, result):
        return parse_receipt(result, default_bank="BCA")["result"]

    def test_interbank_transfer(self):
        r = self.parse(wondr_transfer("BCA"))
        self.assertEqual(r["bank"], "BNI")
        self.assertEqual(r["jenis_transaksi"], "Transfer Antar Bank")
        self.assertTrue(r["isTransferAntarBank"])
        self.assertEqual(r["from_account"], "*******123")  # mask kept
        self.assertEqual(r["transfer_antarbank_detail"],
                         {"name": "JOHN DOE", "amount": 250000, "fee": 2500, "description": None,
                          "to_account": "1234567890", "to_bank": "BCA"})
        self.assertEqual(r["transaction_datetime"], "2026-01-15T12:30:45+07:00")

    def test_raw_fields(self):
        raw = parse_receipt(wondr_transfer(), default_bank="BCA")["raw"]
        self.assertEqual(raw["Bank Penerima"], "BCA")
        self.assertEqual(raw["Rekening Penerima"], "1234567890")
        self.assertEqual(raw["Biaya transaksi"], 2500)
        self.assertEqual(raw["Total"], 252500)
        self.assertEqual(raw["Metode transfer"], "BI-FAST")
        self.assertEqual(raw["Ref ID"], "20260115123045000001")

    def test_same_bank_transfer_is_domestik(self):
        r = self.parse(wondr_transfer("BNI"))
        self.assertEqual(r["jenis_transaksi"], "Transfer Domestik")

    def test_note_becomes_description(self):
        r = self.parse(wondr_transfer(note="bayar kos"))
        self.assertEqual(r["transfer_antarbank_detail"]["description"], "bayar kos")

    def test_bca_receipt_to_bni_stays_bca(self):
        # A BCA receipt for a transfer *to* BNI mentions BNI but must not pick the BNI template.
        rows = [("m-BCA", 300, 40), ("Jenis Transaksi", 100, 100), ("Transfer ke BNI", 500, 100),
                ("Bank Tujuan", 100, 160), ("BNI", 500, 160), ("Dari Rekening", 100, 220), ("123", 500, 220)]
        self.assertEqual(self.parse(paddle_result(rows))["bank"], "BCA")


class TestWatermarkFilter(unittest.TestCase):
    def test_tilted_lines_are_dropped(self):
        result = [{
            "rec_texts": ["JOHN DOE", "BCA"],
            "rec_scores": [0.99, 0.99],
            "rec_polys": [
                [[0, 0], [100, 0], [100, 20], [0, 20]],     # horizontal
                [[0, 60], [80, 0], [95, 20], [15, 80]],     # ~37° like the logo watermark
            ],
        }]
        self.assertEqual([l.text for l in lines_from_paddle(result)], ["JOHN DOE"])
        self.assertEqual(len(lines_from_paddle(result, max_tilt=180)), 2)


if __name__ == "__main__":
    unittest.main()
