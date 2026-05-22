package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Expo Push REST. The single send endpoint accepts up to 100 messages per
// request and is documented at https://docs.expo.dev/push-notifications/sending-notifications.
const (
	expoSendURL     = "https://exp.host/--/api/v2/push/send"
	expoBatchSize   = 100
	expoHTTPTimeout = 15 * time.Second
)

// Message is the per-recipient payload we send to Expo. Channel ID is the
// android notification channel registered by the app at startup.
type Message struct {
	To        string         `json:"to"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Data      map[string]any `json:"data,omitempty"`
	Sound     string         `json:"sound,omitempty"`
	Priority  string         `json:"priority,omitempty"`
	ChannelID string         `json:"channelId,omitempty"`
}

type Ticket struct {
	Status  string         `json:"status"`
	ID      string         `json:"id,omitempty"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type tickets struct {
	Data   []Ticket          `json:"data"`
	Errors []json.RawMessage `json:"errors,omitempty"`
}

type ExpoClient struct {
	http        *http.Client
	accessToken string
}

func NewExpoClient(accessToken string) *ExpoClient {
	return &ExpoClient{
		http:        &http.Client{Timeout: expoHTTPTimeout},
		accessToken: accessToken,
	}
}

// SendResult is aggregated across every batch of a Send call.
type SendResult struct {
	Delivered     int
	Failed        int
	InvalidTokens []string
}

// Send dispatches the same notification to many tokens. We chunk into
// expoBatchSize, accumulate counts, and collect any tokens whose ticket comes
// back with DeviceNotRegistered so the caller can prune them from the DB.
func (c *ExpoClient) Send(ctx context.Context, tokens []string, title, body string, data map[string]any) (SendResult, error) {
	var res SendResult
	if len(tokens) == 0 {
		return res, nil
	}
	for start := 0; start < len(tokens); start += expoBatchSize {
		end := start + expoBatchSize
		if end > len(tokens) {
			end = len(tokens)
		}
		batch := make([]Message, 0, end-start)
		for _, t := range tokens[start:end] {
			batch = append(batch, Message{
				To:        t,
				Title:     title,
				Body:      body,
				Data:      data,
				Sound:     "default",
				Priority:  "high",
				ChannelID: "default",
			})
		}
		tickets, err := c.send(ctx, batch)
		if err != nil {
			slog.Error("expo push: batch transport error", "err", err, "count", len(batch))
			res.Failed += len(batch)
			continue
		}
		for i, tk := range tickets.Data {
			if tk.Status == "ok" {
				res.Delivered++
				continue
			}
			res.Failed++
			errCode, _ := tk.Details["error"].(string)
			to := ""
			if i < len(batch) {
				to = maskToken(batch[i].To)
			}
			// Surface every per-ticket failure — the dashboard summary only
			// has a counter, so the server log is the only place to see the
			// real cause (MismatchSenderId / InvalidCredentials / etc).
			slog.Error("expo push: ticket failed",
				"to", to,
				"status", tk.Status,
				"code", errCode,
				"message", tk.Message,
				"details", tk.Details,
			)
			if errCode == "DeviceNotRegistered" && i < len(batch) {
				res.InvalidTokens = append(res.InvalidTokens, batch[i].To)
			}
		}
	}
	return res, nil
}

func (c *ExpoClient) send(ctx context.Context, msgs []Message) (*tickets, error) {
	body, err := json.Marshal(msgs)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoSendURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("expo push: %s: %s", resp.Status, string(raw))
	}
	var t tickets
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	if len(t.Errors) > 0 {
		slog.Error("expo push: top-level errors", "errors", t.Errors)
		return &t, errors.New("expo push returned errors")
	}
	return &t, nil
}

// maskToken keeps just enough of the token to recognize it in logs without
// leaking the full registration string.
func maskToken(t string) string {
	if len(t) <= 14 {
		return t
	}
	return t[:14] + "…" + t[len(t)-4:]
}
