# telegram-bot (Go)

Telegram bot that turns a receipt photo into a [Sure](https://github.com/we-promise/sure)
transaction, using [`ocr-server`](../ocr-server) to read the receipt:

```
photo ─► getFile ─► ocr-server /ocr ─► Sure GET /accounts, POST /transactions
      ─► reply (details + one button per Sure category) ─► tap ─► Sure PUT /transactions/:id
```

- **Long polling:** no webhook, no public URL; works behind NAT.
- **Standard library only:** no third-party Go modules.
- **Small:** ~10 MB image (`FROM scratch`), ~2 MB RAM when idle.
- **Private:** only chats in `TELEGRAM_ALLOWED_CHAT_IDS` can use it.
- **One message per receipt:** "⏳ Memproses struk…" is edited into the result, then into the chosen category.
- **Plain-text replies:** OCR text with `<` or `&` can't break a message.

## Configuration

| Variable | Default | |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | — | **required** |
| `SURE_API_URL` | — | **required**, e.g. `http://sure-web:3000/api/v1` |
| `SURE_API_KEY` | — | **required** |
| `TELEGRAM_ALLOWED_CHAT_IDS` | *(everyone)* | Comma-separated chat IDs. A chat that isn't listed gets a reply with its chat ID, so you can add it |
| `OCR_URL` | `http://ocr-server:8001` | |
| `MAX_CONCURRENT_OCR` | `1` | Receipts OCR'd at the same time (OCR is CPU heavy) |
| `HEALTH_ADDR` | `:8080` | `GET /health` returns 503 if polling Telegram has stalled |
| `LOG_LEVEL` | `info` | `debug` for more |
| `TELEGRAM_API_URL` | `https://api.telegram.org` | For tests / a local Bot API server |

In Docker these come from the root [`.env`](../.env.example) via `docker-compose.yaml` (`BOT_LOG_LEVEL` there becomes `LOG_LEVEL`). To run the bot outside Docker, export them in your shell.

> **One consumer per bot token.** Telegram delivers updates either to a webhook or to long
> polling. On start the bot deletes any webhook set for its token, and two bot instances with
> the same token make `getUpdates` fail with `409 Conflict`. Use a separate token per deployment.

## Behaviour

| Case | Reply |
|---|---|
| Not a photo or image file | "Kirim foto struk …" |
| OCR request fails | "Gagal membaca struk ❌" + error |
| Unknown type, amount unreadable, no account match, Sure error | "Gagal Menambahkan di Sure ❌", Jenis, Alasan |
| Saved | "Pencatatan Berhasil ✅" with Jenis, Tanggal, Tujuan/Merchant/Produk, Jumlah (`Rp45.000`), Biaya + Total when there is a fee, Berita, Dari Rekening + category buttons |
| Category tapped | toast + message ends with "🏷 Kategori: Food ✅", buttons removed |
| "⏭ Tanpa kategori" | message ends with "🏷 Tanpa kategori" |
| Transfer to one of your own Sure accounts | outflow + inflow of the Nominal (Sure auto-matches them into a Transfer) and a fee expense; reply adds "🔁 Transfer ke rekening sendiri"; buttons categorize the fee |

What is sent to Sure:

| Field | Value |
|---|---|
| `date` | Receipt date; today (WIB) if missing or in the future |
| `name` | Recipient / merchant / product, plus ` - <Berita>` when there is one |
| `nature` | `expense` (money out) or `income` (the destination side of an own-account transfer) |
| `amount` | Parsed amount in Rupiah **plus the admin fee** for transfers (`Nominal + Biaya`). Own-account transfer: Nominal on both sides (Sure pairs them into a Transfer) plus a separate fee expense |
| `notes` | Transaction type, or `RRN: …` for QRIS; with a fee: `… · Nominal Rp100.000 + Biaya Rp2.500` |
| `currency` | `IDR` |

Category buttons carry `c:<transaction>:<category>` or `s:<transaction>` in `callback_data`,
with each UUID packed into 22 url-safe base64 characters to stay under Telegram's 64-byte limit.

## Develop

```bash
go test -race ./...
# end-to-end against a running ocr-server and a real receipt (Telegram and Sure are faked):
OCR_E2E_URL=http://localhost:8001 OCR_E2E_IMAGE=/path/receipt.jpg OCR_E2E_ACCOUNT="BCA 123-4**-**89" \
  go test -run E2E -v
```

| File | |
|---|---|
| `main.go` | config, polling loop, health server, graceful shutdown |
| `bot.go` | message and button handlers |
| `receipt.go` | OCR result → transaction fields, reply text, date rules, button encoding |
| `telegram.go`, `ocr.go`, `sure.go` | minimal API clients |
