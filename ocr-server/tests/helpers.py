"""Build fake PaddleOCR results so parsing can be tested without the model."""


def paddle_result(rows):
    """rows: list of (text, x_center, y_center)."""
    texts, scores, polys = [], [], []
    for text, x, y in rows:
        texts.append(text)
        scores.append(0.99)
        polys.append([[x - 20, y - 8], [x + 20, y - 8], [x + 20, y + 8], [x - 20, y + 8]])
    return [{"rec_texts": texts, "rec_scores": scores, "rec_polys": polys}]


def two_column(pairs, header=("m-BCA",), left_x=100, right_x=500, start_y=100, gap=60):
    """Lay out (label, value) pairs BCA-style. value may be a list for multi-line values."""
    rows = [(h, 300, 40 + i * 20) for i, h in enumerate(header)]
    y = start_y
    for label, value in pairs:
        rows.append((label, left_x, y))
        values = value if isinstance(value, list) else [value]
        for j, v in enumerate(values):
            rows.append((v, right_x, y + j * 25))
        y += gap + 25 * (len(values) - 1)
    return paddle_result(rows)
