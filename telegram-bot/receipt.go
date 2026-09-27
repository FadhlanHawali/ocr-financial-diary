package main

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// WIB is the time zone printed on Indonesian bank receipts.
var WIB = time.FixedZone("WIB", 7*60*60)

// Receipt is the bank-agnostic view of an OCR result used by the bot.
type Receipt struct {
	Jenis       string
	Counterpart string // recipient, merchant or product
	CounterRole string // label for Counterpart in the reply
	Amount      int64  // transfer amount as printed (e.g. Nominal), without the fee
	Fee         int64  // admin fee (e.g. BCA "Biaya", BNI "Biaya transaksi"), 0 if none
	Description string // Berita
	ToAccount   string // destination account number (transfers), digits only
	ToBank      string
	Notes       string
	FromAccount string
	Date        string // YYYY-MM-DD used for Sure
	DateTime    string // "YYYY-MM-DD HH:MM" for display, or Date
}

var errUnknownType = errors.New("jenis transaksi tidak dikenali")

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// BuildReceipt maps an OCR result to the transaction fields for its type.
func BuildReceipt(r *OCRResult, now time.Time) (Receipt, error) {
	rc := Receipt{Jenis: r.JenisTransaksi, Notes: r.JenisTransaksi, FromAccount: deref(r.FromAccount)}
	var d *Detail
	switch {
	case r.IsVA && r.TransferVADetail != nil:
		d = r.TransferVADetail
		rc.Counterpart, rc.CounterRole = firstNonEmpty(deref(d.Name), "Transfer VA"), "Tujuan"
	case r.IsTransferDomestik && r.TransferDomestikDetail != nil:
		d = r.TransferDomestikDetail
		rc.Counterpart, rc.CounterRole = firstNonEmpty(deref(d.Name), "Transfer Domestik"), "Tujuan"
		rc.Description = deref(d.Description)
		rc.ToAccount, rc.ToBank = deref(d.ToAccount), deref(d.ToBank)
	case r.IsTransferAntarBank && r.TransferAntarBankDetail != nil:
		d = r.TransferAntarBankDetail
		rc.Counterpart, rc.CounterRole = firstNonEmpty(deref(d.Name), "Transfer Antar Bank"), "Tujuan"
		rc.Description = deref(d.Description)
		rc.ToAccount, rc.ToBank = deref(d.ToAccount), deref(d.ToBank)
	case r.IsQris && r.TransferQrisDetail != nil:
		d = r.TransferQrisDetail
		rc.Counterpart, rc.CounterRole = firstNonEmpty(deref(d.Merchant), "Transfer QRIS"), "Merchant"
		if rrn := deref(d.RRN); rrn != "" {
			rc.Notes = "RRN: " + rrn
		}
	case r.IsPln && r.TransferPlnDetail != nil:
		d = r.TransferPlnDetail
		rc.Counterpart, rc.CounterRole = firstNonEmpty(deref(d.Name), "Transfer PLN"), "Produk"
	default:
		return rc, fmt.Errorf("%w: %s", errUnknownType, firstNonEmpty(r.JenisTransaksi, "Unknown"))
	}

	switch {
	case d.Amount.Valid:
		rc.Amount = d.Amount.Value
	case d.Amount.Raw != "":
		return rc, fmt.Errorf("jumlah tidak terbaca: %q", d.Amount.Raw)
	default:
		return rc, errors.New("jumlah tidak ditemukan di struk")
	}
	switch {
	case d.Fee.Valid:
		rc.Fee = d.Fee.Value
	case d.Fee.Raw != "":
		// Better to stop than to record a wrong total.
		return rc, fmt.Errorf("biaya tidak terbaca: %q", d.Fee.Raw)
	}
	if rc.Fee > 0 {
		rc.Notes += fmt.Sprintf(" · Nominal %s + Biaya %s", FormatRupiah(rc.Amount), FormatRupiah(rc.Fee))
	}
	if rc.FromAccount == "" {
		return rc, errors.New("rekening sumber tidak ditemukan di struk")
	}

	rc.Date = ResolveDate(deref(r.TransactionDate), now)
	rc.DateTime = rc.Date
	if dt, err := time.Parse(time.RFC3339, deref(r.TransactionDatetime)); err == nil && dt.In(WIB).Format("2006-01-02") == rc.Date {
		rc.DateTime = dt.In(WIB).Format("2006-01-02 15:04")
	}
	return rc, nil
}

// ResolveDate uses the receipt date, or today (WIB) when it is missing, invalid or in the future.
func ResolveDate(receiptDate string, now time.Time) string {
	today := now.In(WIB).Format("2006-01-02")
	d, err := time.Parse("2006-01-02", receiptDate)
	if err != nil || d.Format("2006-01-02") > today {
		return today
	}
	return d.Format("2006-01-02")
}

// FormatRupiah formats 1500000 as "Rp1.500.000".
func FormatRupiah(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprint(v)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-Rp" + b.String()
	}
	return "Rp" + b.String()
}

// Total is what left the account and what is recorded in Sure: amount + fee.
func (rc Receipt) Total() int64 {
	return rc.Amount + rc.Fee
}

// SuccessText is the reply after the transaction is saved.
func (rc Receipt) SuccessText() string {
	lines := []string{
		"Pencatatan Berhasil ✅",
		"Jenis: " + rc.Jenis,
		"Tanggal: " + rc.DateTime,
		rc.CounterRole + ": " + rc.Counterpart,
		"Jumlah: " + FormatRupiah(rc.Amount),
	}
	if rc.Fee > 0 {
		lines = append(lines, "Biaya: "+FormatRupiah(rc.Fee), "Total: "+FormatRupiah(rc.Total()))
	}
	if rc.Description != "" {
		lines = append(lines, "Berita: "+rc.Description)
	}
	lines = append(lines, "Dari Rekening: "+rc.FromAccount)
	return strings.Join(lines, "\n")
}

// --- Category buttons -------------------------------------------------------
//
// Telegram limits callback_data to 64 bytes. Sure ids are UUIDs, packed into 22 url-safe
// base64 chars (url-safe alphabet, no padding):
//   "c:<txn>:<category>"  set category (47 bytes)
//   "s:<txn>"             skip          (24 bytes)

const (
	CategoryPrompt     = "🏷 Pilih kategori:"
	buttonsPerRow      = 2
	maxCategoryButtons = 98 // Telegram allows 100 buttons per message; one row is "skip"
)

func EncodeID(id string) (string, error) {
	b, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(b) != 16 {
		return "", fmt.Errorf("not a UUID: %q", id)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func DecodeID(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 16 {
		return "", fmt.Errorf("bad id %q", s)
	}
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

func BuildCategoryKeyboard(transactionID string, categories []Category) (*InlineKeyboard, error) {
	txn, err := EncodeID(transactionID)
	if err != nil {
		return nil, err
	}
	sorted := append([]Category(nil), categories...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Label()) < strings.ToLower(sorted[j].Label())
	})
	var buttons []InlineButton
	for _, c := range sorted {
		if len(buttons) == maxCategoryButtons {
			break
		}
		cid, err := EncodeID(c.ID)
		if err != nil {
			continue
		}
		buttons = append(buttons, InlineButton{Text: c.Label(), CallbackData: "c:" + txn + ":" + cid})
	}
	kb := &InlineKeyboard{}
	for i := 0; i < len(buttons); i += buttonsPerRow {
		kb.InlineKeyboard = append(kb.InlineKeyboard, buttons[i:min(i+buttonsPerRow, len(buttons))])
	}
	kb.InlineKeyboard = append(kb.InlineKeyboard, []InlineButton{{Text: "⏭ Tanpa kategori", CallbackData: "s:" + txn}})
	return kb, nil
}

type CallbackAction struct {
	Skip          bool
	TransactionID string
	CategoryID    string
}

func ParseCallback(data string) (CallbackAction, error) {
	parts := strings.Split(data, ":")
	switch {
	case len(parts) == 2 && parts[0] == "s":
		txn, err := DecodeID(parts[1])
		return CallbackAction{Skip: true, TransactionID: txn}, err
	case len(parts) == 3 && parts[0] == "c":
		txn, err := DecodeID(parts[1])
		if err != nil {
			return CallbackAction{}, err
		}
		cat, err := DecodeID(parts[2])
		return CallbackAction{TransactionID: txn, CategoryID: cat}, err
	}
	return CallbackAction{}, fmt.Errorf("invalid callback data %q", data)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
