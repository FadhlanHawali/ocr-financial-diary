package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

// Amount holds a detail amount. ocr-server returns a number, or the raw text when it
// could not parse the amount, or null.
type Amount struct {
	Value int64
	Valid bool
	Raw   string
}

func (a *Amount) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		return nil
	case strings.HasPrefix(s, `"`):
		return json.Unmarshal(b, &a.Raw)
	default:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		a.Value, a.Valid = int64(f), true
		return nil
	}
}

type Detail struct {
	Name        *string `json:"name"`
	Merchant    *string `json:"merchant"`
	Description *string `json:"description"`
	RRN         *string `json:"rrn"`
	Amount      Amount  `json:"amount"`
	Fee         Amount  `json:"fee"`        // admin fee (transfers); null when the receipt shows none
	ToAccount   *string `json:"to_account"` // destination account number, digits only (transfers)
	ToBank      *string `json:"to_bank"`
}

// OCRResult mirrors the ocr-server /ocr response.
type OCRResult struct {
	JenisTransaksi          string  `json:"jenis_transaksi"`
	IsVA                    bool    `json:"isVA"`
	IsTransferDomestik      bool    `json:"isTransferDomestik"`
	IsTransferAntarBank     bool    `json:"isTransferAntarBank"`
	IsQris                  bool    `json:"isQris"`
	IsPln                   bool    `json:"isPln"`
	FromAccount             *string `json:"from_account"`
	TransferVADetail        *Detail `json:"transfer_va_detail"`
	TransferDomestikDetail  *Detail `json:"transfer_domestik_detail"`
	TransferAntarBankDetail *Detail `json:"transfer_antarbank_detail"`
	TransferQrisDetail      *Detail `json:"transfer_qris_detail"`
	TransferPlnDetail       *Detail `json:"transfer_pln_detail"`
	TransactionDate         *string `json:"transaction_date"`
	TransactionDatetime     *string `json:"transaction_datetime"`
	Bank                    *string `json:"bank"`
}

type OCRClient struct {
	baseURL string
	http    *http.Client
}

func NewOCRClient(baseURL string, hc *http.Client) *OCRClient {
	return &OCRClient{baseURL: strings.TrimRight(baseURL, "/"), http: hc}
}

// Scan uploads an image to ocr-server and returns the parsed receipt.
func (c *OCRClient) Scan(ctx context.Context, image []byte, filename, contentType string) (*OCRResult, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", contentType) // ocr-server rejects anything that isn't image/*
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(image); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/ocr", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocr-server: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocr-server HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var r OCRResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("ocr-server: bad response: %w", err)
	}
	return &r, nil
}
