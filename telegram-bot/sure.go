package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// SureClient talks to the Sure REST API (/api/v1).
type SureClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewSureClient(baseURL, apiKey string, hc *http.Client) *SureClient {
	return &SureClient{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: hc}
}

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Category struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Parent *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"parent"`
}

// Label is "Parent › Child" for subcategories, otherwise the name.
func (c Category) Label() string {
	if c.Parent != nil && c.Parent.Name != "" {
		return c.Parent.Name + " › " + c.Name
	}
	return c.Name
}

// Transaction natures understood by Sure's API: expense is stored as a positive amount
// (money out), income as a negative one (money in).
const (
	NatureExpense = "expense"
	NatureIncome  = "income"
)

type TransactionInput struct {
	AccountID   string
	Nature      string // NatureExpense or NatureIncome; empty = amount sign as given
	Date        string // YYYY-MM-DD
	Amount      int64
	Name        string
	Description string
	Notes       string
}

type pagination struct {
	Page       int `json:"page"`
	TotalPages int `json:"total_pages"`
}

func (s *SureClient) do(ctx context.Context, method, path string, query url.Values, payload, out any, okStatus int) error {
	u := s.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", s.apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("sure %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != okStatus {
		return fmt.Errorf("sure %s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(data), 300))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("sure %s %s: bad response: %w", method, path, err)
		}
	}
	return nil
}

// listAll follows Sure's page/per_page pagination and decodes items under key.
func listAll[T any](ctx context.Context, s *SureClient, path, key string) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		var raw map[string]json.RawMessage
		q := url.Values{"page": {fmt.Sprint(page)}, "per_page": {"100"}}
		if err := s.do(ctx, http.MethodGet, path, q, nil, &raw, http.StatusOK); err != nil {
			return nil, err
		}
		var items []T
		if err := json.Unmarshal(raw[key], &items); err != nil && raw[key] != nil {
			return nil, fmt.Errorf("sure %s: bad %q: %w", path, key, err)
		}
		all = append(all, items...)
		var p pagination
		if raw["pagination"] != nil {
			_ = json.Unmarshal(raw["pagination"], &p)
		}
		if p.TotalPages <= page || len(items) == 0 {
			return all, nil
		}
	}
}

func (s *SureClient) Accounts(ctx context.Context) ([]Account, error) {
	return listAll[Account](ctx, s, "/accounts", "accounts")
}

func (s *SureClient) Categories(ctx context.Context) ([]Category, error) {
	return listAll[Category](ctx, s, "/categories", "categories")
}

// FindAccount matches the receipt's account string against Sure account names
// (case-insensitive, either contains the other).
func FindAccount(accounts []Account, fromAccount string) (Account, bool) {
	from := strings.ToLower(strings.TrimSpace(fromAccount))
	if from == "" {
		return Account{}, false
	}
	for _, a := range accounts {
		name := strings.ToLower(strings.TrimSpace(a.Name))
		if name == "" {
			continue
		}
		if strings.Contains(name, from) || strings.Contains(from, name) {
			return a, true
		}
	}
	return Account{}, false
}

var accountNumberRe = regexp.MustCompile(`[0-9*][0-9* \-]*[0-9*]`)

// MatchesAccountNumber reports whether an account name contains the full account number
// `number` (digits only), either written out or as a bank-style mask in which '*' stands for
// one hidden digit: "BCA 123-4**-**90" matches "1234567890", "BNI *******789" matches
// "9876543789". At least 3 visible digits are required so a name like "***" matches nothing.
func MatchesAccountNumber(name, number string) bool {
	if number == "" {
		return false
	}
	for _, tok := range accountNumberRe.FindAllString(name, -1) {
		pattern := strings.NewReplacer("-", "", " ", "").Replace(tok)
		if strings.Count(pattern, "*") == 0 {
			// A full number must be the whole number, not part of a longer one.
			if pattern == number {
				return true
			}
			continue
		}
		if len(pattern) != len(number) || len(pattern)-strings.Count(pattern, "*") < 3 {
			continue
		}
		ok := true
		for i := 0; i < len(pattern); i++ {
			if pattern[i] != '*' && pattern[i] != number[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// FindAccountsByNumber returns every Sure account whose name matches the full account number.
func FindAccountsByNumber(accounts []Account, number string) []Account {
	var out []Account
	for _, a := range accounts {
		if MatchesAccountNumber(a.Name, number) {
			out = append(out, a)
		}
	}
	return out
}

// DeleteTransaction removes a transaction (used to undo half of a failed own-account transfer).
func (s *SureClient) DeleteTransaction(ctx context.Context, transactionID string) error {
	err := s.do(ctx, http.MethodDelete, "/transactions/"+url.PathEscape(transactionID), nil, nil, nil, http.StatusOK)
	if err != nil && strings.Contains(err.Error(), "HTTP 204") {
		return nil
	}
	return err
}

// CreateTransaction creates the transaction and returns its id.
func (s *SureClient) CreateTransaction(ctx context.Context, in TransactionInput) (string, error) {
	name := in.Name
	if in.Description != "" {
		name += " - " + in.Description
	}
	payload := map[string]any{"transaction": map[string]any{
		"account_id":  in.AccountID,
		"date":        in.Date,
		"amount":      in.Amount,
		"name":        name,
		"description": in.Description,
		"notes":       in.Notes,
		"currency":    "IDR",
	}}
	if in.Nature != "" {
		payload["transaction"].(map[string]any)["nature"] = in.Nature
	}
	var out struct {
		ID          string `json:"id"`
		Transaction *struct {
			ID string `json:"id"`
		} `json:"transaction"`
	}
	if err := s.do(ctx, http.MethodPost, "/transactions", nil, payload, &out, http.StatusCreated); err != nil {
		return "", err
	}
	if out.ID == "" && out.Transaction != nil {
		out.ID = out.Transaction.ID
	}
	return out.ID, nil
}

// SetCategory changes only the category (Sure ignores omitted fields) and returns its name.
func (s *SureClient) SetCategory(ctx context.Context, transactionID, categoryID string) (string, error) {
	var out struct {
		Category *struct {
			Name string `json:"name"`
		} `json:"category"`
	}
	payload := map[string]any{"transaction": map[string]any{"category_id": categoryID}}
	if err := s.do(ctx, http.MethodPut, "/transactions/"+url.PathEscape(transactionID), nil, payload, &out, http.StatusOK); err != nil {
		return "", err
	}
	if out.Category != nil {
		return out.Category.Name, nil
	}
	return "", nil
}
