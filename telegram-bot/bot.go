package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"
)

const (
	msgNotImage    = "Kirim foto struk (sebagai foto atau file gambar) untuk dicatat."
	msgProcessing  = "⏳ Memproses struk…"
	msgOCRFailed   = "Gagal membaca struk ❌"
	msgSureFailed  = "Gagal Menambahkan di Sure ❌"
	msgNotAllowed  = "Bot ini privat. Chat ID kamu: %d"
	msgSkipped     = "🏷 Tanpa kategori"
	msgCategorySet = "🏷 Kategori: %s ✅"
)

type Bot struct {
	tg      *Telegram
	ocr     *OCRClient
	sure    *SureClient
	allowed map[int64]bool // empty = everyone
	ocrSem  chan struct{}  // limits concurrent OCR (CPU heavy)
	log     *slog.Logger
	now     func() time.Time
}

func (b *Bot) isAllowed(chatID int64) bool {
	return len(b.allowed) == 0 || b.allowed[chatID]
}

func (b *Bot) HandleUpdate(ctx context.Context, u Update) {
	switch {
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		b.handleMessage(ctx, u.Message)
	}
}

// imageOf returns the file to OCR: the largest photo size, or a document with an image MIME type.
func imageOf(m *Message) (fileID, contentType string, ok bool) {
	if n := len(m.Photo); n > 0 {
		return m.Photo[n-1].FileID, "image/jpeg", true
	}
	if m.Document != nil && strings.HasPrefix(m.Document.MimeType, "image/") {
		return m.Document.FileID, m.Document.MimeType, true
	}
	return "", "", false
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	if _, err := b.tg.SendMessage(ctx, chatID, text, nil); err != nil {
		b.log.Error("send message", "chat", chatID, "err", err)
	}
}

func (b *Bot) handleMessage(ctx context.Context, m *Message) {
	chatID := m.Chat.ID
	log := b.log.With("chat", chatID, "message", m.MessageID)
	if !b.isAllowed(chatID) {
		log.Warn("message from chat not in TELEGRAM_ALLOWED_CHAT_IDS")
		b.reply(ctx, chatID, fmt.Sprintf(msgNotAllowed, chatID))
		return
	}
	fileID, contentType, ok := imageOf(m)
	if !ok {
		b.reply(ctx, chatID, msgNotImage)
		return
	}

	progress, err := b.tg.SendMessage(ctx, chatID, msgProcessing, nil)
	if err != nil {
		log.Error("send progress message", "err", err)
		return
	}
	// finish replaces the progress message with the outcome.
	finish := func(text string, kb *InlineKeyboard) {
		if err := b.tg.EditMessageText(ctx, chatID, progress.MessageID, text, kb); err != nil {
			log.Error("edit message", "err", err)
		}
	}

	select {
	case b.ocrSem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	result, err := b.scan(ctx, fileID, contentType)
	<-b.ocrSem
	if err != nil {
		log.Error("ocr failed", "err", err)
		finish(msgOCRFailed+"\n"+truncate(err.Error(), 300), nil)
		return
	}
	log.Info("ocr result", "jenis", result.JenisTransaksi, "bank", deref(result.Bank), "date", deref(result.TransactionDate))

	rc, err := BuildReceipt(result, b.now())
	if err != nil {
		finish(sureFailure(result.JenisTransaksi, err), nil)
		return
	}

	saved, err := b.save(ctx, rc)
	if err != nil {
		log.Error("save to sure failed", "err", err)
		finish(sureFailure(rc.Jenis, err), nil)
		return
	}
	if saved.Destination != nil {
		log.Info("own-account transfer created", "outflow", saved.OutflowID, "inflow", saved.InflowID, "fee", saved.FeeID,
			"from", saved.Source.Name, "to", saved.Destination.Name, "amount", rc.Amount, "fee_amount", rc.Fee, "date", rc.Date)
	} else {
		log.Info("transaction created", "id", saved.OutflowID, "date", rc.Date, "amount", rc.Amount, "fee", rc.Fee, "total", rc.Total())
	}

	text := rc.SuccessText() + saved.Summary(rc)
	target := saved.CategoryTarget()
	if target == "" {
		finish(text, nil) // own-account transfer without fee: nothing to categorize
		return
	}
	kb, err := b.categoryKeyboard(ctx, target)
	if err != nil {
		log.Warn("no category buttons", "err", err)
		finish(text, nil)
		return
	}
	finish(text+"\n\n"+CategoryPrompt, kb)
}

func sureFailure(jenis string, err error) string {
	return fmt.Sprintf("%s\nJenis: %s\nAlasan: %s", msgSureFailed, firstNonEmpty(jenis, "-"), truncate(err.Error(), 300))
}

func (b *Bot) scan(ctx context.Context, fileID, contentType string) (*OCRResult, error) {
	data, filePath, err := b.tg.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	name := path.Base(filePath)
	if name == "." || name == "/" {
		name = "receipt.jpg"
	}
	return b.ocr.Scan(ctx, data, name, contentType)
}

// Saved describes what was written to Sure for one receipt.
type Saved struct {
	OutflowID   string   // money out of the source account (always)
	InflowID    string   // money into the destination account (own-account transfers only)
	FeeID       string   // separate fee expense (own-account transfers with a fee only)
	Source      Account  // Sure account the money left
	Destination *Account // Sure account that received it, when it is one of yours
	Note        string   // why a transfer was *not* treated as own-account, if relevant
}

// CategoryTarget is the transaction the category buttons apply to: the only real expense.
// For an own-account transfer that is the fee (none if there is no fee), otherwise the outflow.
func (s Saved) CategoryTarget() string {
	if s.Destination != nil {
		return s.FeeID
	}
	return s.OutflowID
}

// Summary lists the Sure transactions for the reply (only for own-account transfers).
func (s Saved) Summary(rc Receipt) string {
	if s.Destination == nil {
		if s.Note != "" {
			return "\n" + s.Note
		}
		return ""
	}
	out := fmt.Sprintf("\n\n🔁 Transfer ke rekening sendiri\nTransfer %s: %s → %s",
		FormatRupiah(rc.Amount), s.Source.Name, s.Destination.Name)
	if s.FeeID != "" {
		out += fmt.Sprintf("\nBiaya %s dicatat sebagai pengeluaran", FormatRupiah(rc.Fee))
	}
	return out
}

// save writes the receipt to Sure.
//
// Normal receipt: one expense of amount + fee on the source account.
//
// Transfer to one of your own accounts: an outflow on the source and an inflow on the
// destination, both for the transferred amount (Nominal). Equal amounts on two of your accounts
// within a few days is what Sure's transfer matching looks for, so Sure pairs them into a
// Transfer (shown as "Auto-matched", confirm it with ✓). The admin fee is a separate expense on
// the source account, because a Sure transfer can't have different amounts on its two sides.
func (b *Bot) save(ctx context.Context, rc Receipt) (Saved, error) {
	accounts, err := b.sure.Accounts(ctx)
	if err != nil {
		return Saved{}, err
	}
	acc, ok := FindAccount(accounts, rc.FromAccount)
	if !ok {
		return Saved{}, fmt.Errorf("tidak ada akun Sure yang cocok dengan %q", rc.FromAccount)
	}
	saved := Saved{Source: acc}

	// Is the destination one of your own Sure accounts?
	var dest *Account
	if rc.ToAccount != "" {
		var others []Account
		for _, m := range FindAccountsByNumber(accounts, rc.ToAccount) {
			if m.ID != acc.ID {
				others = append(others, m)
			}
		}
		switch len(others) {
		case 0:
		case 1:
			dest = &others[0]
		default:
			names := make([]string, len(others))
			for i, m := range others {
				names[i] = m.Name
			}
			saved.Note = "⚠️ Rekening tujuan cocok dengan beberapa akun Sure (" + strings.Join(names, ", ") +
				"), jadi hanya dicatat sebagai pengeluaran."
		}
	}

	if dest == nil {
		saved.OutflowID, err = b.sure.CreateTransaction(ctx, TransactionInput{
			AccountID:   acc.ID,
			Nature:      NatureExpense,
			Date:        rc.Date,
			Amount:      rc.Total(), // amount + admin fee
			Name:        rc.Counterpart,
			Description: rc.Description,
			Notes:       rc.Notes,
		})
		return saved, err
	}

	// Own-account transfer. Create the parts in order and undo them all if one fails,
	// so Sure never holds half a transfer.
	var created []string
	undo := func(cause error) error {
		var failed []string
		for i := len(created) - 1; i >= 0; i-- {
			if err := b.sure.DeleteTransaction(ctx, created[i]); err != nil {
				failed = append(failed, created[i])
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("%v; transaksi %s tidak bisa dibatalkan, periksa di Sure", cause, strings.Join(failed, ", "))
		}
		return fmt.Errorf("%w (transfer dibatalkan)", cause)
	}
	create := func(in TransactionInput) (string, error) {
		id, err := b.sure.CreateTransaction(ctx, in)
		if err == nil {
			created = append(created, id)
		}
		return id, err
	}
	note := rc.Jenis + " · Transfer antar rekening sendiri"

	if saved.OutflowID, err = create(TransactionInput{
		AccountID: acc.ID, Nature: NatureExpense, Date: rc.Date, Amount: rc.Amount,
		Name: "Transfer ke " + dest.Name, Description: rc.Description, Notes: note,
	}); err != nil {
		return saved, err
	}
	if saved.InflowID, err = create(TransactionInput{
		AccountID: dest.ID, Nature: NatureIncome, Date: rc.Date, Amount: rc.Amount,
		Name: "Transfer dari " + acc.Name, Description: rc.Description, Notes: note,
	}); err != nil {
		return saved, undo(fmt.Errorf("pemasukan ke %s gagal: %w", dest.Name, err))
	}
	if rc.Fee > 0 {
		if saved.FeeID, err = create(TransactionInput{
			AccountID: acc.ID, Nature: NatureExpense, Date: rc.Date, Amount: rc.Fee,
			Name: "Biaya transfer ke " + dest.Name, Notes: rc.Jenis + " · Biaya transfer",
		}); err != nil {
			return saved, undo(fmt.Errorf("biaya transfer gagal dicatat: %w", err))
		}
	}
	saved.Destination = dest
	return saved, nil
}

func (b *Bot) categoryKeyboard(ctx context.Context, txnID string) (*InlineKeyboard, error) {
	if txnID == "" {
		return nil, errors.New("sure returned no transaction id")
	}
	cats, err := b.sure.Categories(ctx)
	if err != nil {
		return nil, err
	}
	if len(cats) == 0 {
		return nil, errors.New("no categories in Sure")
	}
	return BuildCategoryKeyboard(txnID, cats)
}

func (b *Bot) handleCallback(ctx context.Context, q *CallbackQuery) {
	answer := func(text string, alert bool) {
		if err := b.tg.AnswerCallback(ctx, q.ID, text, alert); err != nil {
			b.log.Error("answer callback", "err", err)
		}
	}
	if q.Message == nil {
		answer("Pesan sudah tidak tersedia", true)
		return
	}
	chatID := q.Message.Chat.ID
	log := b.log.With("chat", chatID, "message", q.Message.MessageID)
	if !b.isAllowed(chatID) {
		answer("Tidak diizinkan", true)
		return
	}
	action, err := ParseCallback(q.Data)
	if err != nil {
		log.Warn("bad callback", "err", err)
		answer("Tombol tidak valid", true)
		return
	}

	result := msgSkipped
	if !action.Skip {
		name, err := b.sure.SetCategory(ctx, action.TransactionID, action.CategoryID)
		if err != nil {
			log.Error("set category failed", "txn", action.TransactionID, "err", err)
			answer("Gagal: "+err.Error(), true) // keep the buttons so the user can retry
			return
		}
		result = fmt.Sprintf(msgCategorySet, firstNonEmpty(name, "dipilih"))
		log.Info("category set", "txn", action.TransactionID, "category", name)
	}
	answer(result, false)

	// Replace the prompt line with the result and remove the buttons.
	base := strings.TrimSuffix(strings.TrimRight(q.Message.Text, "\n "), CategoryPrompt)
	text := strings.TrimRight(base, "\n ") + "\n\n" + result
	if err := b.tg.EditMessageText(ctx, chatID, q.Message.MessageID, text, nil); err != nil {
		log.Error("edit message", "err", err)
	}
}
