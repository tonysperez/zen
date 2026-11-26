// Package slack is a minimal Slack Web API client for zen's Slack task
// watcher and lead channel. It only implements the handful of methods they
// need (reactions.list, conversations.history, conversations.replies,
// reactions.add, chat.postMessage, chat.getPermalink, reminders.add,
// auth.test) — it is not a general-purpose Slack SDK.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://slack.com/api/"

// Client is a thin wrapper around the Slack Web API, authenticated with a
// user token (scopes: see UserScopes).
type Client struct {
	tokens  TokenSource
	http    *http.Client
	baseURL string
}

// NewClient creates a Slack API client authenticated with a fixed token.
func NewClient(token string) *Client {
	return NewClientWithSource(StaticToken(token))
}

// NewClientWithSource creates a Slack API client that asks src for the token
// before every call, so rotating tokens are refreshed transparently.
func NewClientWithSource(src TokenSource) *Client {
	return &Client{
		tokens:  src,
		http:    &http.Client{Timeout: 15 * time.Second},
		baseURL: defaultBaseURL,
	}
}

// WithBaseURL points the client at another Web API root (it must end in
// "/"), such as a test server.
func (c *Client) WithBaseURL(base string) *Client {
	c.baseURL = base
	return c
}

// envelope is the {ok, error} shape every Slack Web API response embeds.
type envelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (c *Client) get(ctx context.Context, method string, params url.Values) ([]byte, error) {
	u := c.baseURL + method
	if params != nil {
		u += "?" + params.Encode()
	}
	return c.call(ctx, method, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	})
}

func (c *Client) post(ctx context.Context, method string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding %s request: %w", method, err)
	}
	return c.call(ctx, method, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+method, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		return req, nil
	})
}

// call authenticates and sends the request newReq builds. If Slack rejects
// the token before its recorded expiry (for example because another zen
// process rotated it), it asks the token source for a new one and retries
// once.
func (c *Client) call(ctx context.Context, method string, newReq func() (*http.Request, error)) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		token, err := c.tokens.Token(ctx)
		if err != nil {
			return nil, err
		}
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		body, err := c.do(req, method)
		var apiErr *APIError
		if attempt == 0 && errors.As(err, &apiErr) && retryableAuthError(apiErr.Code) {
			if inv, ok := c.tokens.(invalidator); ok {
				inv.Invalidate(token)
				continue
			}
		}
		return body, err
	}
}

func retryableAuthError(code string) bool {
	switch code {
	case "token_expired", "token_revoked", "invalid_auth":
		return true
	}
	return false
}

func (c *Client) do(req *http.Request, method string) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s response: %w", method, err)
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parsing %s response: %w", method, err)
	}
	if !env.OK {
		return nil, &APIError{Method: method, Code: env.Error}
	}
	return body, nil
}

// APIError is returned when Slack responds with {"ok": false}.
type APIError struct {
	Method string
	Code   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("slack %s: %s", e.Method, e.Code)
}

// Reaction is a single named reaction on a message, with the count and users
// who added it.
type Reaction struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Users []string `json:"users"`
}

type reactionItem struct {
	Channel string `json:"channel"`
	Message struct {
		Ts        string     `json:"ts"`
		Text      string     `json:"text"`
		Reactions []Reaction `json:"reactions"`
	} `json:"message"`
}

type reactionsListResponse struct {
	Items            []reactionItem `json:"items"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// ReactionHit identifies a message carrying the emoji ListReactions searched for.
type ReactionHit struct {
	ChannelID string
	MessageTS string
}

// ListReactions pages through reactions.list for the authenticated user and
// returns every message that carries a reaction named emoji, across up to
// maxPages pages of 100 items each. reactions.list documents no sort order or
// since-timestamp filter, so callers must dedupe against their own seen-set
// rather than relying on pagination order or an early cutoff.
func (c *Client) ListReactions(ctx context.Context, emoji string, maxPages int) ([]ReactionHit, error) {
	var hits []ReactionHit
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := url.Values{"limit": {"100"}, "full": {"true"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		body, err := c.get(ctx, "reactions.list", params)
		if err != nil {
			return nil, err
		}
		var resp reactionsListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parsing reactions.list response: %w", err)
		}
		for _, item := range resp.Items {
			for _, r := range item.Message.Reactions {
				if r.Name == emoji {
					hits = append(hits, ReactionHit{ChannelID: item.Channel, MessageTS: item.Message.Ts})
				}
			}
		}
		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}
	return hits, nil
}

// Message is a single Slack message, as returned by conversations.history
// and conversations.replies.
type Message struct {
	User string `json:"user"`
	Text string `json:"text"`
	Ts   string `json:"ts"`
	// Thread fields. ThreadTS is set on a thread's parent (equal to Ts) and
	// on its replies; ReplyCount and LatestReply only on the parent.
	ThreadTS    string `json:"thread_ts,omitempty"`
	ReplyCount  int    `json:"reply_count,omitempty"`
	LatestReply string `json:"latest_reply,omitempty"`
	// Subtype is empty for an ordinary message; "thread_broadcast",
	// "channel_join", "bot_message" and so on otherwise.
	Subtype string `json:"subtype,omitempty"`
	BotID   string `json:"bot_id,omitempty"`
}

// ConversationsHistory returns the channel's top-level messages (and thread
// replies broadcast to the channel) posted after oldest, newest first,
// across up to maxPages pages of 200. An empty oldest means no lower bound.
func (c *Client) ConversationsHistory(ctx context.Context, channel, oldest string, maxPages int) ([]Message, error) {
	var all []Message
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := url.Values{"channel": {channel}, "limit": {"200"}}
		if oldest != "" {
			params.Set("oldest", oldest)
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		body, err := c.get(ctx, "conversations.history", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Messages         []Message `json:"messages"`
			ResponseMetadata struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parsing conversations.history response: %w", err)
		}
		all = append(all, resp.Messages...)
		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}
	return all, nil
}

// ThreadRepliesSince returns the messages in a thread posted after oldest,
// oldest first. Slack includes the parent message whatever oldest says; it
// is left out here unless it is itself newer than oldest.
func (c *Client) ThreadRepliesSince(ctx context.Context, channel, threadTS, oldest string) ([]Message, error) {
	params := url.Values{"channel": {channel}, "ts": {threadTS}, "limit": {"200"}}
	if oldest != "" {
		params.Set("oldest", oldest)
	}
	body, err := c.get(ctx, "conversations.replies", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing conversations.replies response: %w", err)
	}
	var out []Message
	for _, m := range resp.Messages {
		if oldest != "" && CompareTS(m.Ts, oldest) <= 0 {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// ConversationsReplies returns the parent message plus all replies in a thread.
func (c *Client) ConversationsReplies(ctx context.Context, channel, ts string) ([]Message, error) {
	params := url.Values{"channel": {channel}, "ts": {ts}, "limit": {"200"}}
	body, err := c.get(ctx, "conversations.replies", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing conversations.replies response: %w", err)
	}
	return resp.Messages, nil
}

// AddReaction adds a reaction to a message. Slack's "already_reacted" error
// is treated as success — idempotent under daemon restarts re-processing the
// same message.
func (c *Client) AddReaction(ctx context.Context, channel, ts, name string) error {
	_, err := c.post(ctx, "reactions.add", map[string]string{
		"channel":   channel,
		"timestamp": ts,
		"name":      name,
	})
	if apiErr, ok := err.(*APIError); ok && apiErr.Code == "already_reacted" {
		return nil
	}
	return err
}

// PostMessage sends a message to a channel or, when channel is a user ID, a
// direct message to that user.
func (c *Client) PostMessage(ctx context.Context, channel, text string) error {
	_, err := c.post(ctx, "chat.postMessage", map[string]string{
		"channel": channel,
		"text":    text,
	})
	return err
}

// Post sends text to channel, as a reply in the thread threadTS when that
// is set, and returns the new message's ts.
func (c *Client) Post(ctx context.Context, channel, text, threadTS string) (string, error) {
	payload := map[string]string{"channel": channel, "text": text}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	body, err := c.post(ctx, "chat.postMessage", payload)
	if err != nil {
		return "", err
	}
	var resp struct {
		Ts string `json:"ts"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing chat.postMessage response: %w", err)
	}
	return resp.Ts, nil
}

// AddReminder creates a Slack reminder for the signed-in user, due at due.
// Slackbot delivers it as a DM, which Slack notifies like any other. Needs
// the reminders:write scope.
func (c *Client) AddReminder(ctx context.Context, text string, due time.Time) error {
	_, err := c.post(ctx, "reminders.add", map[string]any{
		"text": text,
		"time": due.Unix(),
	})
	return err
}

// Permalink returns the permalink URL for a message.
func (c *Client) Permalink(ctx context.Context, channel, ts string) (string, error) {
	params := url.Values{"channel": {channel}, "message_ts": {ts}}
	body, err := c.get(ctx, "chat.getPermalink", params)
	if err != nil {
		return "", err
	}
	var resp struct {
		Permalink string `json:"permalink"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing chat.getPermalink response: %w", err)
	}
	return resp.Permalink, nil
}

// AuthTest returns the Slack user ID the token authenticates as.
func (c *Client) AuthTest(ctx context.Context) (userID string, err error) {
	body, err := c.get(ctx, "auth.test", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing auth.test response: %w", err)
	}
	return resp.UserID, nil
}

// HasReaction reports whether a message already carries a reaction named name.
func (c *Client) HasReaction(ctx context.Context, channel, ts, name string) (bool, error) {
	params := url.Values{"channel": {channel}, "timestamp": {ts}, "full": {"true"}}
	body, err := c.get(ctx, "reactions.get", params)
	if err != nil {
		return false, err
	}
	var resp struct {
		Message struct {
			Reactions []Reaction `json:"reactions"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, fmt.Errorf("parsing reactions.get response: %w", err)
	}
	for _, r := range resp.Message.Reactions {
		if r.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// Revoke revokes the token the client is using.
func (c *Client) Revoke(ctx context.Context) error {
	_, err := c.get(ctx, "auth.revoke", nil)
	return err
}

// TeamInfo is what auth.test reports about the token's user and workspace.
type TeamInfo struct {
	UserID string `json:"user_id"`
	User   string `json:"user"`
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
}

// Identity calls auth.test and returns the user and workspace the token
// belongs to.
func (c *Client) Identity(ctx context.Context) (*TeamInfo, error) {
	body, err := c.get(ctx, "auth.test", nil)
	if err != nil {
		return nil, err
	}
	var info TeamInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parsing auth.test response: %w", err)
	}
	return &info, nil
}

// CompareTS orders two Slack message timestamps ("1700000000.123456"),
// returning -1, 0 or 1. Timestamps compare by their seconds, then by their
// fraction, so differing digit counts still order correctly.
func CompareTS(a, b string) int {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if c := compareDigits(as, bs); c != 0 {
		return c
	}
	// Fractions: pad to the same width on the right.
	for len(af) < len(bf) {
		af += "0"
	}
	for len(bf) < len(af) {
		bf += "0"
	}
	return strings.Compare(af, bf)
}

func compareDigits(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// FormatTS renders t as a Slack message timestamp.
func FormatTS(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}
