"""Regression tests against real receipts kept OUTSIDE the repo (they contain personal data).

Folder layout (point RECEIPT_SAMPLES_DIR at it; one folder per bank is fine):

    <samples>/
      bca-qris-1.jpg
      _ocr_cache/bca-qris-1.jpg.ocr.json   # PaddleOCR output, so tests run without the model
      expected.json                        # {"bca-qris-1.jpg": {"transaction_date": "2025-11-23", ...}}

Only the keys listed in expected.json are compared, so it can start small.
Run:  RECEIPT_SAMPLES_DIR=/path/to/sample/bca python -m unittest tests.test_samples -v
"""
import json
import os
import unittest
from pathlib import Path

from receipt_parser import parse_receipt

SAMPLES = os.getenv("RECEIPT_SAMPLES_DIR")


@unittest.skipUnless(SAMPLES, "set RECEIPT_SAMPLES_DIR to run sample regression tests")
class TestSamples(unittest.TestCase):
    def test_expected_fields(self):
        root = Path(SAMPLES)
        expected = json.loads((root / "expected.json").read_text(encoding="utf-8"))
        self.assertTrue(expected, "expected.json is empty")
        for image, fields in expected.items():
            with self.subTest(image=image):
                cache = root / "_ocr_cache" / f"{image}.ocr.json"
                self.assertTrue(cache.exists(), f"missing OCR cache {cache}")
                ocr_result = json.loads(cache.read_text(encoding="utf-8"))
                result = parse_receipt(ocr_result, default_bank="BCA")["result"]
                for key, value in fields.items():
                    self.assertEqual(result.get(key), value, f"{image}: {key}")


if __name__ == "__main__":
    unittest.main()
