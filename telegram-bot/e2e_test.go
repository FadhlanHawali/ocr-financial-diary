package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestE2ERealOCR runs the photo flow against a real ocr-server with a real receipt image,
// while Telegram and Sure are faked. Skipped unless both env vars are set:
//
//	OCR_E2E_URL=http://localhost:8001 OCR_E2E_IMAGE=/path/receipt.jpg go test -run E2E -v
func TestE2ERealOCR(t *testing.T) {
	url, img := os.Getenv("OCR_E2E_URL"), os.Getenv("OCR_E2E_IMAGE")
	if url == "" || img == "" {
		t.Skip("set OCR_E2E_URL and OCR_E2E_IMAGE to run")
	}
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	b, f := newTestBot(t)
	f.fileData = data
	// OCR_E2E_ACCOUNT is a Sure account name, or several separated by ";" (e.g. to test
	// own-account transfers: "BNI *******789;BCA 123-4**-**90").
	var accs []string
	for i, name := range strings.Split(os.Getenv("OCR_E2E_ACCOUNT"), ";") {
		accs = append(accs, fmt.Sprintf(`{"id":"acc%d","name":%q}`, i+1, name))
	}
	f.accounts = `{"accounts":[` + strings.Join(accs, ",") + `]}`
	b.ocr = NewOCRClient(url, b.ocr.http)
	b.HandleUpdate(context.Background(), photoUpdate(1))

	text, _ := f.last("editMessageText")["text"].(string)
	t.Logf("reply:\n%s", text)
	for i, p := range f.posts {
		t.Logf("sure transaction %d: %v", i+1, p["transaction"])
	}
	if !strings.HasPrefix(text, "Pencatatan Berhasil") {
		t.Fatalf("expected success reply")
	}
}
