# Bank templates

Each file here teaches the OCR server one bank's receipt format.

## Add a new bank

1. **Collect samples.** Send a few receipts to `POST /ocr?debug=true` (you can force a
   template with `&bank=BCA`). The `debug.lines` field shows every OCR line with its
   x/y position, so you can see the labels and whether values sit to the **right** of
   the label or **below** it.
2. **Create `<bank>.py`** in this folder:

```python
from ..base import BankTemplate
from ..layouts import stacked_kv          # or two_column_kv
from ..normalize import clean_account_number, parse_amount
from ..output import UNKNOWN, build_output
from ..registry import register

KEYS = ["Nama Penerima", "Nominal", "Biaya", "Catatan", "Rekening Sumber", ...]

@register
class MandiriTemplate(BankTemplate):
    bank = "MANDIRI"                        # also usable as /ocr?bank=MANDIRI
    detect_keywords = ("Livin", "Mandiri")  # text that only appears on this bank's receipts

    def extract(self, lines):
        raw = stacked_kv(lines, KEYS)
        raw["Nominal"] = parse_amount(raw.get("Nominal"))   # "Rp 1.500.000,00" style by default
        return raw

    def transform(self, raw):
        detail = {"name": raw.get("Nama Penerima"), "amount": raw.get("Nominal"),
                  "description": raw.get("Catatan")}
        return build_output("Transfer Antar Bank", clean_account_number(raw.get("Rekening Sumber")), detail)
```

3. **Register it** by adding `from . import <bank>` to `templates/__init__.py`.
4. **Add a test** in `ocr-server/tests/` (copy `test_bca.py`) and run
   `python -m unittest discover -s tests -t .` from `ocr-server/`.

## How a template is chosen

- `?bank=<id>` on the request forces that template.
- Otherwise every template's `detect()` scores the receipt (one point per distinct
  `detect_keywords` hit) and the highest score wins. Pick keywords that are specific:
  a Mandiri receipt for a transfer *to* BCA also contains "BCA", so a Mandiri template
  needs at least two of its own keywords to outscore the BCA template.
- If nothing scores, `OCR_DEFAULT_BANK` (default `BCA`) is used; set it empty to
  return `Unknown` instead.

## Building blocks

| Helper | Use |
|---|---|
| `layouts.two_column_kv` | label left, value right (BCA) |
| `layouts.stacked_kv` | label on one line, value underneath |
| sections (see `bni.py`) | receipts split into titled blocks ("Penerima", "Sumber dana", …): group lines by header, then parse each block |
| `normalize.parse_amount` | `Rp 1.500.000,00` by default; configurable for `IDR 1,500,000.00` |
| `normalize.clean_account_number` | strips spaces/text around account numbers |
| `normalize.find_datetime` | transaction date/time (`15 Jan 2026 12:30:45`, Indonesian months too); used by default, override `extract_datetime()` if a bank differs |
| `output.build_output` | builds the `/ocr` response (`jenis_transaksi`, flags, detail) |

Label matching is case-insensitive and longest-first, so listing both "Nama" and
"Nama Penerima" is safe.

## Watermarks and OCR settings

- Diagonal logo watermarks ("BCA", "BNI") are removed before templates see the text: lines tilted
  more than 10° are dropped in `lines.lines_from_paddle` (receipt text is ~0–3°, watermarks 15–42°).
- Page unwarping is off by default (`OCR_DOC_UNWARPING=false`); on app screenshots it crops the
  left edge ("Penerima" → "enerima"). Collect `debug.lines` with the same setting the server uses.
