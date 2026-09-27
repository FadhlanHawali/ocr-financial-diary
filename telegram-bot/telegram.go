package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Telegram is a minimal Bot API client covering only what the bot uses.
type Telegram struct {
	apiBase  string // https://api.telegram.org/bot<token>
	fileBase string // https://api.telegram.org/file/bot<token>
	token    string
	http     *http.Client
}

func NewTelegram(apiURL, token string, hc *http.Client) *Telegram {
	apiURL = strings.TrimRight(apiURL, "/")
	return &Telegram{
		apiBase:  apiURL + "/bot" + token,
		fileBase: apiURL + "/file/bot" + token,
		token:    token,
		http:     hc,
	}
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Message struct {
	MessageID int64       `json:"message_id"`
	Chat      Chat        `json:"chat"`
	From      *User       `json:"from,omitempty"`
	Text      string      `json:"text,omitempty"`
	Photo     []PhotoSize `json:"photo,omitempty"`
	Document  *Document   `json:"document,omitempty"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

type PhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

// redact removes the bot token from errors: net/http errors include the request URL.
func (t *Telegram) redact(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.ReplaceAll(err.Error(), t.token, "<token>"))
}

func (t *Telegram) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.apiBase+"/"+method, bytes.NewReader(body))
	if err != nil {
		return t.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, t.redact(err))
	}
	defer resp.Body.Close()

	var r apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d, bad response: %w", method, resp.StatusCode, err)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %d %s", method, r.ErrorCode, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (t *Telegram) DeleteWebhook(ctx context.Context) error {
	return t.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}

func (t *Telegram) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var updates []Update
	err := t.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	return updates, err
}

func (t *Telegram) SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboard) (*Message, error) {
	params := map[string]any{"chat_id": chatID, "text": text}
	if kb != nil {
		params["reply_markup"] = kb
	}
	var m Message
	if err := t.call(ctx, "sendMessage", params, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// EditMessageText replaces a message's text. A nil keyboard removes any buttons.
func (t *Telegram) EditMessageText(ctx context.Context, chatID, messageID int64, text string, kb *InlineKeyboard) error {
	params := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text}
	if kb != nil {
		params["reply_markup"] = kb
	}
	return t.call(ctx, "editMessageText", params, nil)
}

func (t *Telegram) AnswerCallback(ctx context.Context, id, text string, alert bool) error {
	if len([]rune(text)) > 190 {
		text = string([]rune(text)[:190])
	}
	return t.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": id, "text": text, "show_alert": alert,
	}, nil)
}

// DownloadFile resolves a file_id and returns its bytes.
func (t *Telegram) DownloadFile(ctx context.Context, fileID string) ([]byte, string, error) {
	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := t.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.fileBase+"/"+f.FilePath, nil)
	if err != nil {
		return nil, "", t.redact(err)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("telegram download: %w", t.redact(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("telegram download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20)) // Bot API files are at most 20 MB
	return data, f.FilePath, err
}
