// Package notify sends notifications through the Pushover REST API.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIURL is the Pushover message endpoint.
const DefaultAPIURL = "https://api.pushover.net/1/messages.json"

// Priorities used by the app.
const (
	PriorityNormal    = 0
	PriorityEmergency = 2
)

// Notification sounds used by the app (see https://pushover.net/api#sounds).
const (
	SoundPositive = "good-1"  // good news: the notice changed (error gone)
	SoundNegative = "alert-2" // bad news: the error notice is back
)

// Emergency-priority parameters: Pushover re-alerts the device every
// emergencyRetry seconds until acknowledged, giving up after the client's
// expire duration. Expire follows the app's re-send cadence (ALERT_INTERVAL)
// so device-side retries neither stop early nor stack across the app's own
// repeats; Pushover bounds it to [retry, 3h].
const (
	emergencyRetry     = 60 * time.Second
	emergencyExpireMax = 3 * time.Hour // Pushover API maximum (10800s)
)

// Message is one notification to deliver.
type Message struct {
	Title    string
	Body     string
	Priority int
	Sound    string // empty means the device's default sound
}

// Client is a minimal Pushover API client.
type Client struct {
	apiURL string
	token  string
	user   string
	expire time.Duration // emergency-priority expire, clamped
	http   *http.Client
	log    *slog.Logger
}

// New builds a Client for the official Pushover endpoint. alertInterval is
// the app's re-send cadence; emergency-priority messages use it as their
// expire, clamped to Pushover's accepted range.
func New(token, user string, alertInterval time.Duration, log *slog.Logger) *Client {
	return &Client{
		apiURL: DefaultAPIURL,
		token:  token,
		user:   user,
		expire: min(max(alertInterval, emergencyRetry), emergencyExpireMax),
		http:   &http.Client{Timeout: 30 * time.Second},
		log:    log,
	}
}

// NewWithURL is like New but targets a custom endpoint. Used in tests.
func NewWithURL(apiURL, token, user string, alertInterval time.Duration, log *slog.Logger) *Client {
	c := New(token, user, alertInterval, log)
	c.apiURL = apiURL
	return c
}

type apiResponse struct {
	Status  int      `json:"status"`
	Request string   `json:"request"`
	Errors  []string `json:"errors"`
}

// Send delivers one message. It returns an error if the API is unreachable
// or rejects the message.
func (c *Client) Send(ctx context.Context, m Message) error {
	form := url.Values{
		"token":    {c.token},
		"user":     {c.user},
		"title":    {m.Title},
		"message":  {m.Body},
		"priority": {strconv.Itoa(m.Priority)},
	}
	if m.Priority == PriorityEmergency {
		form.Set("retry", strconv.Itoa(int(emergencyRetry.Seconds())))
		form.Set("expire", strconv.Itoa(int(c.expire.Seconds())))
	}
	if m.Sound != "" {
		form.Set("sound", m.Sound)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Error("pushover request failed", "err", err, "title", m.Title)
		return fmt.Errorf("calling Pushover API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var parsed apiResponse
	_ = json.Unmarshal(body, &parsed)

	if resp.StatusCode != http.StatusOK || parsed.Status != 1 {
		c.log.Error("pushover rejected message",
			"http_status", resp.StatusCode,
			"api_status", parsed.Status,
			"api_errors", strings.Join(parsed.Errors, "; "),
			"request_id", parsed.Request,
			"title", m.Title,
		)
		if len(parsed.Errors) > 0 {
			return fmt.Errorf("pushover rejected message: %s", strings.Join(parsed.Errors, "; "))
		}
		return fmt.Errorf("pushover returned HTTP %d", resp.StatusCode)
	}

	c.log.Info("pushover message sent",
		"title", m.Title,
		"priority", m.Priority,
		"request_id", parsed.Request,
	)
	return nil
}
