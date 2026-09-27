package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testToken = "123:SECRET"
	txnID     = "327ed41c-3883-430c-bf8d-daccb51e0cb3"
	foodID    = "11111111-2222-4333-8444-555555555555"
	inflowID  = "99999999-8888-4777-8666-555555555555"
	feeID     = "77777777-6666-4555-8444-333333333333"
)

type call struct {
	Method string
	Body   map[string]any
}

// fakeWorld is one HTTP server playing Telegram, ocr-server and Sure.
type fakeWorld struct {
	t         *testing.T
	mu        sync.Mutex
	calls     []call
	ocrJSON   string
	ocrCT     string
	accounts  string
	putBody   map[string]any
	postBody  map[string]any
	posts     []map[string]any // every POST /transactions body, in order
	deleted   []string
	sureFail  bool
	failPostN int    // if > 0, the n-th POST /transactions fails
	fileData  []byte // bytes served as the Telegram photo
}

func (f *fakeWorld) record(method string, body map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{method, body})
}

func (f *fakeWorld) last(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == method {
			return f.calls[i].Body
		}
	}
	return nil
}

func (f *fakeWorld) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/bot"+testToken+"/"):
		method := strings.TrimPrefix(p, "/bot"+testToken+"/")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.record(method, body)
		var result any = true
		switch method {
		case "getFile":
			result = map[string]any{"file_path": "photos/file_1.jpg"}
		case "sendMessage":
			result = map[string]any{"message_id": 42, "chat": map[string]any{"id": body["chat_id"]}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	case p == "/file/bot"+testToken+"/photos/file_1.jpg":
		if f.fileData != nil {
			_, _ = w.Write(f.fileData)
			return
		}
		_, _ = w.Write([]byte("JPEGDATA"))
	case p == "/ocr":
		file, hdr, err := r.FormFile("file")
		if err != nil {
			f.t.Errorf("ocr: %v", err)
			http.Error(w, "no file", 400)
			return
		}
		data, _ := io.ReadAll(file)
		f.mu.Lock()
		f.ocrCT = hdr.Header.Get("Content-Type")
		f.mu.Unlock()
		if string(data) != "JPEGDATA" {
			f.t.Errorf("ocr got %q", data)
		}
		_, _ = io.WriteString(w, f.ocrJSON)
	case p == "/api/v1/accounts":
		if r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		_, _ = io.WriteString(w, f.accounts)
	case p == "/api/v1/categories":
		page := r.URL.Query().Get("page")
		cats := `[{"id":"` + foodID + `","name":"Food","parent":null}]`
		if page == "2" {
			cats = `[{"id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","name":"Groceries","parent":{"id":"x","name":"Food"}}]`
		}
		fmt.Fprintf(w, `{"categories":%s,"pagination":{"page":%s,"total_pages":2}}`, cats, page)
	case p == "/api/v1/transactions" && r.Method == http.MethodPost:
		if f.sureFail {
			http.Error(w, `{"error":"boom"}`, 500)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.posts = append(f.posts, body)
		n := len(f.posts)
		f.postBody = body
		fail := f.failPostN == n
		f.mu.Unlock()
		if fail {
			http.Error(w, `{"error":"boom"}`, 500)
			return
		}
		id := []string{txnID, inflowID, feeID}[min(n, 3)-1]
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"`+id+`","name":"x"}`)
	case strings.HasPrefix(p, "/api/v1/transactions/") && r.Method == http.MethodDelete:
		f.mu.Lock()
		f.deleted = append(f.deleted, strings.TrimPrefix(p, "/api/v1/transactions/"))
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"message":"deleted"}`)
	case p == "/api/v1/transactions/"+txnID && r.Method == http.MethodPut:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.putBody = body
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"id":"`+txnID+`","category":{"id":"`+foodID+`","name":"Food"}}`)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, p)
		http.NotFound(w, r)
	}
}

const qrisOCR = `{"jenis_transaksi":"Transfer QRIS","isVA":false,"isTransferDomestik":false,"isTransferAntarBank":false,"isQris":true,"isPln":false,
 "from_account":"123-4**-**90","transfer_qris_detail":{"merchant":"WARUNG MAJU BCA","amount":45000,"rrn":"123456789"},
 "transaction_date":"2026-01-14","transaction_datetime":"2026-01-14T12:01:20+07:00","bank":"BCA"}`

func newTestBot(t *testing.T, allowed ...int64) (*Bot, *fakeWorld) {
	f := &fakeWorld{t: t, ocrJSON: qrisOCR, accounts: `{"accounts":[{"id":"acc1","name":"BCA 123-4**-**90"}],"pagination":{"page":1,"total_pages":1}}`}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	al := map[int64]bool{}
	for _, id := range allowed {
		al[id] = true
	}
	b := &Bot{
		tg:      NewTelegram(srv.URL, testToken, srv.Client()),
		ocr:     NewOCRClient(srv.URL, srv.Client()),
		sure:    NewSureClient(srv.URL+"/api/v1", "key", srv.Client()),
		allowed: al,
		ocrSem:  make(chan struct{}, 1),
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:     func() time.Time { return time.Date(2026, 9, 26, 10, 0, 0, 0, WIB) },
	}
	return b, f
}

func photoUpdate(chat int64) Update {
	return Update{UpdateID: 1, Message: &Message{MessageID: 7, Chat: Chat{ID: chat}, Photo: []PhotoSize{{FileID: "small"}, {FileID: "big"}}}}
}

func TestPhotoFlowCreatesTransactionAndShowsButtons(t *testing.T) {
	b, f := newTestBot(t, 99)
	b.HandleUpdate(context.Background(), photoUpdate(99))

	if got := f.last("getFile")["file_id"]; got != "big" {
		t.Errorf("should download largest photo, got %v", got)
	}
	if f.ocrCT != "image/jpeg" {
		t.Errorf("ocr content type = %q", f.ocrCT)
	}
	tx := f.postBody["transaction"].(map[string]any)
	want := map[string]any{"account_id": "acc1", "date": "2026-01-14", "amount": float64(45000),
		"name": "WARUNG MAJU BCA", "notes": "RRN: 123456789", "currency": "IDR", "description": ""}
	for k, v := range want {
		if tx[k] != v {
			t.Errorf("transaction[%s] = %v, want %v", k, tx[k], v)
		}
	}
	if f.last("sendMessage")["text"] != msgProcessing {
		t.Errorf("first reply should be the progress message")
	}
	edit := f.last("editMessageText")
	text := edit["text"].(string)
	for _, s := range []string{"Pencatatan Berhasil ✅", "Tanggal: 2026-01-14 12:01", "Merchant: WARUNG MAJU BCA", "Jumlah: Rp45.000", "Dari Rekening: 123-4**-**90", CategoryPrompt} {
		if !strings.Contains(text, s) {
			t.Errorf("reply missing %q:\n%s", s, text)
		}
	}
	kb := edit["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	first := kb[0].([]any)
	if len(first) != 2 || first[0].(map[string]any)["text"] != "Food" || first[1].(map[string]any)["text"] != "Food › Groceries" {
		t.Errorf("unexpected first row %v", first)
	}
	skip := kb[len(kb)-1].([]any)[0].(map[string]any)
	if skip["callback_data"] != "s:Mn7UHDiDQwy_jdrMtR4Msw" {
		t.Errorf("skip callback = %v", skip["callback_data"])
	}
}

const antarBankOCR = `{"jenis_transaksi":"Transfer Antar Bank","isVA":false,"isTransferDomestik":false,"isTransferAntarBank":true,"isQris":false,"isPln":false,
 "from_account":"123-4**-**90","transfer_antarbank_detail":{"name":"JOHN DOE","amount":100000,"fee":2500,"description":"bayar kos"},
 "transaction_date":"2026-01-15","transaction_datetime":"2026-01-15T12:30:45+07:00","bank":"BNI"}`

func TestTransferFeeIsAddedToSureAmount(t *testing.T) {
	b, f := newTestBot(t)
	f.ocrJSON = antarBankOCR
	b.HandleUpdate(context.Background(), photoUpdate(1))

	tx := f.postBody["transaction"].(map[string]any)
	if tx["amount"] != float64(102500) {
		t.Errorf("Sure amount = %v, want 102500 (100000 + 2500 fee)", tx["amount"])
	}
	if tx["notes"] != "Transfer Antar Bank · Nominal Rp100.000 + Biaya Rp2.500" {
		t.Errorf("notes = %q", tx["notes"])
	}
	text := f.last("editMessageText")["text"].(string)
	for _, s := range []string{"Jumlah: Rp100.000", "Biaya: Rp2.500", "Total: Rp102.500", "Berita: bayar kos"} {
		if !strings.Contains(text, s) {
			t.Errorf("reply missing %q:\n%s", s, text)
		}
	}
}

func TestNoFeeKeepsAmount(t *testing.T) {
	b, f := newTestBot(t)
	f.ocrJSON = strings.Replace(antarBankOCR, `"fee":2500`, `"fee":null`, 1)
	b.HandleUpdate(context.Background(), photoUpdate(1))
	if tx := f.postBody["transaction"].(map[string]any); tx["amount"] != float64(100000) || tx["notes"] != "Transfer Antar Bank" {
		t.Errorf("transaction = %v", tx)
	}
	if text := f.last("editMessageText")["text"].(string); strings.Contains(text, "Biaya:") || strings.Contains(text, "Total:") {
		t.Errorf("no fee lines expected:\n%s", text)
	}
}

func TestCategoryTap(t *testing.T) {
	b, f := newTestBot(t)
	txn, _ := EncodeID(txnID)
	cat, _ := EncodeID(foodID)
	b.HandleUpdate(context.Background(), Update{CallbackQuery: &CallbackQuery{
		ID: "q1", Data: "c:" + txn + ":" + cat,
		Message: &Message{MessageID: 42, Chat: Chat{ID: 99}, Text: "Pencatatan Berhasil ✅\nJumlah: Rp45.000\n\n" + CategoryPrompt},
	}})
	if got := f.putBody["transaction"].(map[string]any); len(got) != 1 || got["category_id"] != foodID {
		t.Errorf("PUT body = %v", got)
	}
	if a := f.last("answerCallbackQuery"); a["text"] != "🏷 Kategori: Food ✅" || a["show_alert"] != false {
		t.Errorf("answer = %v", a)
	}
	edit := f.last("editMessageText")
	if edit["text"] != "Pencatatan Berhasil ✅\nJumlah: Rp45.000\n\n🏷 Kategori: Food ✅" {
		t.Errorf("edited text = %q", edit["text"])
	}
	if _, has := edit["reply_markup"]; has {
		t.Errorf("buttons should be removed")
	}
}

func TestSkipTap(t *testing.T) {
	b, f := newTestBot(t)
	txn, _ := EncodeID(txnID)
	b.HandleUpdate(context.Background(), Update{CallbackQuery: &CallbackQuery{
		ID: "q1", Data: "s:" + txn, Message: &Message{MessageID: 42, Chat: Chat{ID: 99}, Text: "X\n\n" + CategoryPrompt},
	}})
	if f.putBody != nil {
		t.Errorf("skip must not call Sure")
	}
	if edit := f.last("editMessageText"); edit["text"] != "X\n\n"+msgSkipped {
		t.Errorf("edited text = %q", edit["text"])
	}
}

func TestChatNotAllowed(t *testing.T) {
	b, f := newTestBot(t, 1)
	b.HandleUpdate(context.Background(), photoUpdate(99))
	if f.last("getFile") != nil || f.postBody != nil {
		t.Errorf("must not process receipts from other chats")
	}
	if !strings.Contains(f.last("sendMessage")["text"].(string), "99") {
		t.Errorf("should tell the chat id")
	}
}

func TestNonImageMessage(t *testing.T) {
	b, f := newTestBot(t)
	b.HandleUpdate(context.Background(), Update{Message: &Message{Chat: Chat{ID: 5}, Text: "halo"}})
	if f.last("sendMessage")["text"] != msgNotImage {
		t.Errorf("expected not-image reply")
	}
	// image sent as a file is accepted, with its own MIME type
	b.HandleUpdate(context.Background(), Update{Message: &Message{Chat: Chat{ID: 5}, Document: &Document{FileID: "doc", MimeType: "image/png"}}})
	if f.ocrCT != "image/png" {
		t.Errorf("document content type = %q", f.ocrCT)
	}
}

func TestFailures(t *testing.T) {
	cases := []struct {
		name, ocr, accounts string
		sureFail            bool
		want                string
	}{
		{name: "unknown type", ocr: `{"jenis_transaksi":"Unknown"}`, want: "jenis transaksi tidak dikenali"},
		{name: "unparsed amount", ocr: strings.Replace(qrisOCR, `"amount":45000`, `"amount":"IDR 45,000"`, 1), want: "jumlah tidak terbaca"},
		{name: "unparsed fee", ocr: strings.Replace(antarBankOCR, `"fee":2500`, `"fee":"Rp2.5OO"`, 1), want: "biaya tidak terbaca"},
		{name: "no account", accounts: `{"accounts":[{"id":"a","name":"Mandiri 999"}]}`, want: "tidak ada akun Sure yang cocok"},
		{name: "sure error", sureFail: true, want: "HTTP 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, f := newTestBot(t)
			if tc.ocr != "" {
				f.ocrJSON = tc.ocr
			}
			if tc.accounts != "" {
				f.accounts = tc.accounts
			}
			f.sureFail = tc.sureFail
			b.HandleUpdate(context.Background(), photoUpdate(1))
			text := f.last("editMessageText")["text"].(string)
			if !strings.HasPrefix(text, msgSureFailed) || !strings.Contains(text, tc.want) {
				t.Errorf("reply = %q, want failure containing %q", text, tc.want)
			}
		})
	}
}

func TestResolveDate(t *testing.T) {
	now := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC) // 26 Sep 03:00 WIB
	for in, want := range map[string]string{
		"2026-09-24": "2026-09-24",
		"2026-09-26": "2026-09-26", // today in WIB, although still the 25th in UTC
		"2026-09-27": "2026-09-26", // future -> today
		"":           "2026-09-26",
		"garbage":    "2026-09-26",
	} {
		if got := ResolveDate(in, now); got != want {
			t.Errorf("ResolveDate(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestIDEncodingMatchesPython(t *testing.T) {
	// expected value = Python's base64.urlsafe_b64encode(uuid.UUID(txnID).bytes).rstrip(b"=")
	got, err := EncodeID(txnID)
	if err != nil || got != "Mn7UHDiDQwy_jdrMtR4Msw" {
		t.Fatalf("EncodeID = %q, %v", got, err)
	}
	back, err := DecodeID(got)
	if err != nil || back != txnID {
		t.Fatalf("DecodeID = %q, %v", back, err)
	}
	if _, err := ParseCallback("c:bad"); err == nil {
		t.Error("expected error for malformed callback")
	}
}

func TestFormatRupiah(t *testing.T) {
	for in, want := range map[int64]string{0: "Rp0", 999: "Rp999", 1000: "Rp1.000", 3000000: "Rp3.000.000", 167693: "Rp167.693"} {
		if got := FormatRupiah(in); got != want {
			t.Errorf("FormatRupiah(%d) = %s, want %s", in, got, want)
		}
	}
}

func TestTokenNotLeakedInErrors(t *testing.T) {
	tg := NewTelegram("http://127.0.0.1:1", testToken, &http.Client{Timeout: time.Second})
	_, err := tg.SendMessage(context.Background(), 1, "x", nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error leaks token or is nil: %v", err)
	}
}

func TestKeyboardLimit(t *testing.T) {
	var cats []Category
	for i := 0; i < 150; i++ {
		cats = append(cats, Category{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Name: fmt.Sprintf("C%03d", i)})
	}
	kb, err := BuildCategoryKeyboard(txnID, cats)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			n++
			if len(btn.CallbackData) > 64 {
				t.Errorf("callback_data too long: %d", len(btn.CallbackData))
			}
		}
	}
	if n != 99 { // 98 categories + skip
		t.Errorf("buttons = %d", n)
	}
}

// --- Own-account transfers ---------------------------------------------------

const ownAccounts = `{"accounts":[
 {"id":"src","name":"BNI *******789"},
 {"id":"dst","name":"BCA 123-4**-**90"},
 {"id":"other","name":"Mandiri 1234567890123"}]}`

func ownTransferOCR(jenis, toAccount string, fee string) string {
	flag := map[string]string{"Transfer Antar Bank": "isTransferAntarBank", "Transfer Domestik": "isTransferDomestik"}[jenis]
	detail := map[string]string{"Transfer Antar Bank": "transfer_antarbank_detail", "Transfer Domestik": "transfer_domestik_detail"}[jenis]
	return `{"jenis_transaksi":"` + jenis + `","` + flag + `":true,"from_account":"*******789",
 "` + detail + `":{"name":"JOHN DOE","amount":100000,"fee":` + fee + `,"description":null,"to_account":"` + toAccount + `","to_bank":"BCA"},
 "transaction_date":"2026-01-15","transaction_datetime":"2026-01-15T12:30:45+07:00","bank":"BNI"}`
}

func TestOwnAccountInterbankTransferIsSureTransferPlusFee(t *testing.T) {
	b, f := newTestBot(t)
	f.accounts = ownAccounts
	f.ocrJSON = ownTransferOCR("Transfer Antar Bank", "1234567890", "2500")
	b.HandleUpdate(context.Background(), photoUpdate(1))

	if len(f.posts) != 3 {
		t.Fatalf("POST /transactions calls = %d, want 3 (outflow, inflow, fee)", len(f.posts))
	}
	out := f.posts[0]["transaction"].(map[string]any)
	in := f.posts[1]["transaction"].(map[string]any)
	fee := f.posts[2]["transaction"].(map[string]any)
	// Outflow and inflow have the same amount so Sure matches them into a Transfer.
	if out["account_id"] != "src" || out["nature"] != "expense" || out["amount"] != float64(100000) {
		t.Errorf("outflow = %v, want src / expense / 100000", out)
	}
	if in["account_id"] != "dst" || in["nature"] != "income" || in["amount"] != float64(100000) {
		t.Errorf("inflow = %v, want dst / income / 100000", in)
	}
	if fee["account_id"] != "src" || fee["nature"] != "expense" || fee["amount"] != float64(2500) ||
		fee["name"] != "Biaya transfer ke BCA 123-4**-**90" {
		t.Errorf("fee = %v, want src / expense / 2500", fee)
	}
	if out["name"] != "Transfer ke BCA 123-4**-**90" || in["name"] != "Transfer dari BNI *******789" {
		t.Errorf("names = %q / %q", out["name"], in["name"])
	}
	for _, tx := range []map[string]any{out, in, fee} {
		if tx["date"] != "2026-01-15" {
			t.Errorf("date = %v", tx["date"])
		}
	}
	edit := f.last("editMessageText")
	text := edit["text"].(string)
	for _, s := range []string{"🔁 Transfer ke rekening sendiri", "Transfer Rp100.000: BNI *******789 → BCA 123-4**-**90",
		"Biaya Rp2.500 dicatat sebagai pengeluaran", CategoryPrompt} {
		if !strings.Contains(text, s) {
			t.Errorf("reply missing %q:\n%s", s, text)
		}
	}
	// the category buttons belong to the fee, the only real expense
	kb := edit["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	skip := kb[len(kb)-1].([]any)[0].(map[string]any)["callback_data"].(string)
	if want, _ := EncodeID(feeID); skip != "s:"+want {
		t.Errorf("buttons point at %q, want the fee transaction", skip)
	}
}

func TestOwnAccountDomestikTransferWithoutFee(t *testing.T) {
	b, f := newTestBot(t)
	f.accounts = ownAccounts
	f.ocrJSON = ownTransferOCR("Transfer Domestik", "1234567890", "null")
	b.HandleUpdate(context.Background(), photoUpdate(1))
	if len(f.posts) != 2 {
		t.Fatalf("POST /transactions calls = %d, want 2 (no fee)", len(f.posts))
	}
	out := f.posts[0]["transaction"].(map[string]any)
	in := f.posts[1]["transaction"].(map[string]any)
	if out["amount"] != float64(100000) || out["nature"] != "expense" || in["amount"] != float64(100000) || in["nature"] != "income" {
		t.Errorf("outflow %v / inflow %v", out, in)
	}
	edit := f.last("editMessageText")
	if _, has := edit["reply_markup"]; has || strings.Contains(edit["text"].(string), CategoryPrompt) {
		t.Errorf("no category buttons expected for a fee-less own transfer:\n%s", edit["text"])
	}
}

func TestTransferToSomeoneElseStaysSingleExpense(t *testing.T) {
	b, f := newTestBot(t)
	f.accounts = ownAccounts
	f.ocrJSON = ownTransferOCR("Transfer Antar Bank", "9990001112", "2500") // same shape, different digits
	b.HandleUpdate(context.Background(), photoUpdate(1))
	if len(f.posts) != 1 {
		t.Fatalf("POST /transactions calls = %d, want 1", len(f.posts))
	}
	tx := f.posts[0]["transaction"].(map[string]any)
	if tx["nature"] != "expense" || tx["amount"] != float64(102500) || tx["name"] != "JOHN DOE" {
		t.Errorf("transaction = %v", tx)
	}
	if strings.Contains(f.last("editMessageText")["text"].(string), "rekening sendiri") {
		t.Error("should not be reported as an own-account transfer")
	}
}

func TestAmbiguousDestinationFallsBackToExpense(t *testing.T) {
	b, f := newTestBot(t)
	f.accounts = `{"accounts":[{"id":"src","name":"BNI *******789"},{"id":"a","name":"BCA 123-4**-**90"},{"id":"b","name":"BCA Tabungan 123-4**-**90"}]}`
	f.ocrJSON = ownTransferOCR("Transfer Antar Bank", "1234567890", "2500")
	b.HandleUpdate(context.Background(), photoUpdate(1))
	if len(f.posts) != 1 {
		t.Fatalf("POST /transactions calls = %d, want 1", len(f.posts))
	}
	if !strings.Contains(f.last("editMessageText")["text"].(string), "beberapa akun Sure") {
		t.Error("reply should explain the ambiguous destination")
	}
}

func TestFailedOwnTransferIsUndone(t *testing.T) {
	for _, tc := range []struct {
		failAt      int
		wantDeleted []string
	}{
		{2, []string{txnID}},           // inflow failed: remove the outflow
		{3, []string{inflowID, txnID}}, // fee failed: remove inflow, then outflow
	} {
		b, f := newTestBot(t)
		f.accounts = ownAccounts
		f.failPostN = tc.failAt
		f.ocrJSON = ownTransferOCR("Transfer Antar Bank", "1234567890", "2500")
		b.HandleUpdate(context.Background(), photoUpdate(1))
		if strings.Join(f.deleted, ",") != strings.Join(tc.wantDeleted, ",") {
			t.Errorf("fail at %d: deleted = %v, want %v", tc.failAt, f.deleted, tc.wantDeleted)
		}
		text := f.last("editMessageText")["text"].(string)
		if !strings.HasPrefix(text, msgSureFailed) || !strings.Contains(text, "transfer dibatalkan") {
			t.Errorf("fail at %d: reply = %q", tc.failAt, text)
		}
	}
}

func TestMatchesAccountNumber(t *testing.T) {
	cases := []struct {
		name, number string
		want         bool
	}{
		{"BCA 123-4**-**90", "1234567890", true},
		{"BCA 123-4**-**90", "1234567891", false},
		{"BCA 123-4**-**90", "12345678900", false}, // length differs
		{"BNI *******789", "9876543789", true},
		{"BNI *******789", "876543789", false},
		{"Mandiri 1234567890123", "1234567890123", true},
		{"Mandiri 1234 5678 9012 3", "1234567890123", true},
		{"Mandiri 1234567890123", "1234567890", false}, // only part of a longer number
		{"Cash", "1234567890", false},
		{"Card ****", "1234", false}, // too few visible digits
	}
	for _, c := range cases {
		if got := MatchesAccountNumber(c.name, c.number); got != c.want {
			t.Errorf("MatchesAccountNumber(%q, %q) = %v, want %v", c.name, c.number, got, c.want)
		}
	}
}
