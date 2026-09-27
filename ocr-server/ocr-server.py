import logging
import os

import cv2
import numpy as np
import uvicorn
from fastapi import FastAPI, File, HTTPException, Query, UploadFile
from fastapi.responses import JSONResponse
from paddleocr import PaddleOCR

from receipt_parser import available_banks, parse_receipt

logging.basicConfig(level=os.getenv("LOG_LEVEL", "INFO"))
logger = logging.getLogger("ocr-server")

OCR_DEVICE = os.getenv("OCR_DEVICE", "gpu")
OCR_LANG = os.getenv("OCR_LANG", "id")
# Used when no template recognises the receipt. Empty -> respond "Unknown".
DEFAULT_BANK = os.getenv("OCR_DEFAULT_BANK", "BCA") or None
# Page unwarping (UVDoc) is meant for photos of paper. On app screenshots it crops the left
# edge ("Penerima" -> "enerima"), so it is off by default.
DOC_UNWARPING = os.getenv("OCR_DOC_UNWARPING", "false").strip().lower() in ("1", "true", "yes")

app = FastAPI(title="Bank Receipt OCR API")

ocr = PaddleOCR(use_angle_cls=True, lang=OCR_LANG, device=OCR_DEVICE, use_doc_unwarping=DOC_UNWARPING)


@app.post("/ocr")
async def ocr_receipt(
    file: UploadFile = File(...),
    bank: str | None = Query(None, description="Force a bank template, e.g. BCA"),
    debug: bool = Query(False, description="Include raw fields and OCR lines in the response"),
):
    if not (file.content_type or "").startswith("image/"):
        raise HTTPException(status_code=400, detail="File must be an image")

    contents = await file.read()
    img = cv2.imdecode(np.frombuffer(contents, np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        raise HTTPException(status_code=400, detail="Could not decode image")

    ocr_result = ocr.predict(img)
    try:
        parsed = parse_receipt(ocr_result, bank_hint=bank, default_bank=DEFAULT_BANK)
    except KeyError:
        raise HTTPException(
            status_code=400,
            detail=f"Unknown bank '{bank}'. Available: {', '.join(available_banks())}",
        )

    result = parsed["result"]
    logger.info("raw fields: %s", parsed["raw"])
    logger.info("result: %s", result)

    if debug:
        result = {
            **result,
            "debug": {
                "raw": parsed["raw"],
                "lines": [
                    {"text": l.text, "x": round(l.x), "y": round(l.y), "score": round(l.score, 3)}
                    for l in parsed["lines"]
                ],
            },
        }
    return JSONResponse(content=result)


@app.get("/banks")
def banks():
    return {"banks": available_banks(), "default": DEFAULT_BANK}


@app.get("/health")
def health():
    return {"status": "ok"}


if __name__ == "__main__":
    uvicorn.run("ocr-server:app", host="0.0.0.0", port=8001, reload=False)
