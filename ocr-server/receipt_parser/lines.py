from __future__ import annotations

import math
from dataclasses import dataclass

# Receipt text is horizontal (measured: at most ~3° on real screenshots), while the diagonal
# bank-logo watermarks behind it ("BCA", "BNI") are read at 15–42°. Lines tilted more than
# this are dropped so watermark fragments don't end up in names and notes.
MAX_TILT_DEGREES = 10.0


@dataclass
class OcrLine:
    """One recognised text line with the centre point of its bounding box."""

    text: str
    x: float
    y: float
    score: float = 1.0


def tilt_degrees(poly) -> float:
    """Angle of the box's top edge (point 0 -> point 1) against the horizontal."""
    return abs(math.degrees(math.atan2(poly[1][1] - poly[0][1], poly[1][0] - poly[0][0])))


def lines_from_paddle(ocr_result, max_tilt: float = MAX_TILT_DEGREES) -> list[OcrLine]:
    """Convert PaddleOCR 3.x ``predict()`` output into OcrLines sorted top-to-bottom.

    Tilted lines (watermarks) are skipped; pass ``max_tilt=180`` to keep everything.
    """
    lines: list[OcrLine] = []
    for res in ocr_result:
        for text, score, poly in zip(res["rec_texts"], res["rec_scores"], res["rec_polys"]):
            if tilt_degrees(poly) > max_tilt:
                continue
            x_center = (poly[0][0] + poly[2][0]) / 2
            y_center = (poly[0][1] + poly[2][1]) / 2
            lines.append(OcrLine(text=str(text).strip(), x=float(x_center), y=float(y_center), score=float(score)))
    lines.sort(key=lambda l: l.y)
    return lines


def full_text(lines: list[OcrLine]) -> str:
    return "\n".join(l.text for l in lines)
