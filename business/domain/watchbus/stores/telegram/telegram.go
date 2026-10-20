// Package telegram sends notifications through the Telegram Bot API. It only
// sends: it never reads updates, so nothing anyone writes to the bot has any
// effect.
//
// The bot token is part of the request path, so every error that leaves this
// package has the token removed, including the URL a failed request carries.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/loadkpi/baton/business/domain/watchbus"
)

// timeout bounds a send, so that a network that does not answer cannot hold up
// the next look at the Sessions.
const timeout = 10 * time.Second

// Settings say where to send. Token is a secret.
type Settings struct {
	// APIURL is the base URL of the Bot API.
	APIURL string
	ChatID string
	Token  string
}

// Source reads the settings. It is called for every message, so a change of
// the config applies to a running watcher.
type Source func(ctx context.Context) (Settings, error)

// Notifier sends a Message with sendMessage.
type Notifier struct {
	source Source
	client *http.Client
}

// New creates the notifier.
func New(source Source) *Notifier {
	return &Notifier{
		source: source,
		client: &http.Client{
			Timeout: timeout,
			// The Bot API does not redirect. Following one would send the
			// request, token included, wherever the answer points.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// sendMessage is the body of the request. Plain text: no parse mode, so
// nothing in a Task name can be taken for markup.
type sendMessage struct {
	ChatID any    `json:"chat_id"`
	Text   string `json:"text"`
}

// Notify sends the status, the Task and the Agent of the message to the chat.
func (n *Notifier) Notify(ctx context.Context, m watchbus.Message) error {
	s, err := n.source(ctx)
	if err != nil {
		return &watchbus.Failure{Kind: "settings", Err: err}
	}
	// A secret must not leak through an error however it got there.
	fail := func(kind string, err error) error {
		return &watchbus.Failure{Kind: kind, Err: errors.New(redact(err.Error(), s.Token))}
	}

	// A numeric chat is sent as a number, an @name as a string.
	var chatID any = s.ChatID
	if id, err := strconv.ParseInt(s.ChatID, 10, 64); err == nil {
		chatID = id
	}
	body, err := json.Marshal(sendMessage{ChatID: chatID, Text: m.Summary() + "\n" + m.Body()})
	if err != nil {
		return fail("request", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	endpoint := s.APIURL + "/bot" + s.Token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fail("request", describe(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fail("connection", describe(err))
	}
	defer func() { _ = resp.Body.Close() }()

	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Sprintf("http %d", resp.StatusCode), errors.New(httpMessage(resp.StatusCode, reply)))
	}
	return nil
}

// describe says what went wrong with a request without the URL that Go puts
// into the error: "Post <url>: <reason>" becomes "send: <reason>".
func describe(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("send: %w", ue.Err)
	}
	return err
}

// httpMessage is "HTTP 401: Unauthorized": the status, and the description the
// Bot API gives, if it gives one. The description is cut short.
func httpMessage(status int, reply []byte) string {
	msg := fmt.Sprintf("HTTP %d", status)
	var r struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(reply, &r) == nil && r.Description != "" {
		d := r.Description
		if len(d) > 200 {
			d = d[:200] + "..."
		}
		msg += ": " + d
	}
	return msg
}

// redact removes the token from text, in the forms it takes in a URL.
func redact(text, token string) string {
	if token == "" {
		return text
	}
	for _, form := range []string{token, url.PathEscape(token), url.QueryEscape(token)} {
		text = strings.ReplaceAll(text, form, "[token]")
	}
	return text
}
