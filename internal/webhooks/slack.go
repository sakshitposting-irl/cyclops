/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package webhooks delivers the report to a webhook receiver (ADR 0007, ADR 0017).

package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sakshitposting-irl/cyclops/internal/render"
)

const (
	maxAttempts   = 4                // the first try and up to 3 retries (ADR 0017)
	maxRetryAfter = 30 * time.Second // the longest we will wait because Slack asked us to
)

// backoff is the wait before the first retry. Each retry doubles it: 1s, 2s, 4s. A var so tests can shrink it.
var backoff = time.Second

// partGap is the wait between the parts of a split report, to stay under Slack's limit of about one
// message per second per webhook (ADR 0017). A var so tests can shrink it.
var partGap = time.Second

// Message is the JSON body posted to the webhook. Text is the fallback shown in notifications.
type Message struct {
	Text   string  `json:"text"`
	Blocks []Block `json:"blocks,omitempty"`
}

type Slack struct {
	name   string
	url    string
	client *http.Client
}

func NewSlack(name, url string) (*Slack, error) {
	// remove \n from url, to avoid encoding error leaking webhook in secret.
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("slack: empty webhook url")
	}
	if name == "" {
		return nil, fmt.Errorf("slack: name is needed for the webhook")
	}
	return &Slack{name: name, url: url, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

// String and GoString keep the URL out of %v, %+v, %s and %#v, so logging a Slack never prints the webhook.
// They use a value receiver so that both Slack and *Slack are covered.
func (s Slack) String() string { return fmt.Sprintf("slack target %q", s.name) }

func (s Slack) GoString() string { return s.String() }

func (s Slack) Send(ctx context.Context, data render.TemplateData) error {
	msgs := buildMessages(data)
	// The parts go out in order. If one still fails after its retries we stop: the earlier parts stay
	// posted, and the error says which part failed.
	for idx, msg := range msgs {
		if idx > 0 {
			if err := sleep(ctx, partGap); err != nil {
				return fmt.Errorf("slack target %q: before part %d of %d: %w", s.name, idx+1, len(msgs), err)
			}
		}
		body, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("slack target %q: encoding part %d: %w", s.name, idx+1, err)
		}
		if err := s.postWithRetry(ctx, body); err != nil {
			return fmt.Errorf("slack target %q: part %d of %d: %w", s.name, idx+1, len(msgs), err)
		}
	}
	return nil
}

// postWithRetry posts body, trying again after a failure that could be temporary. It returns the last
// error once the failure is permanent, the tries run out, or ctx is done.
func (s Slack) postWithRetry(ctx context.Context, body []byte) error {
	var err error
	for i := range maxAttempts {
		var retry bool
		var wait time.Duration
		retry, wait, err = s.post(ctx, body)
		if err == nil || !retry || i == maxAttempts-1 {
			return err
		}

		delay := wait // Slack's Retry-After replaces the backoff for this one wait
		if delay == 0 {
			delay = backoff << i // 1s, 2s, 4s, plus up to half again of jitter
			// every bitleft shift = adds a 0 to the binary rep and mul. by 2
			if half := delay / 2; half > 0 { // rand.N panics on 0
				delay += rand.N(half)
			}
		}
		if serr := sleep(ctx, delay); serr != nil {
			return serr
		}
	}
	return err
}

// post makes one attempt. retry says whether trying again could help; wait is Slack's Retry-After, or 0.
// The errors never contain the URL.
func (s Slack) post(ctx context.Context, body []byte) (retry bool, wait time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		// No %w: this error quotes the URL.
		return false, 0, errors.New("building request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// Do wraps the cause in a *url.Error, which holds the whole URL. Keep only the cause.
		if ue, ok := errors.AsType[*neturl.Error](err); ok {
			err = ue.Err
		}
		return ctx.Err() == nil, 0, fmt.Errorf("sending: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 == 2 {
		return false, 0, nil
	} else if resp.StatusCode == http.StatusTooManyRequests {
		return true, retryAfter(resp), fmt.Errorf("status %d", resp.StatusCode)
	} else if resp.StatusCode >= 500 {
		return true, 0, fmt.Errorf("status %d", resp.StatusCode)
	} else {
		// Any other 4xx means the webhook or the payload is wrong; sending it again will not fix that.
		return false, 0, fmt.Errorf("status %d", resp.StatusCode)
	}
}

// retryAfter reads the Retry-After header as a number of seconds, capped at maxRetryAfter.
// It returns 0 if the header is absent or not a positive whole number.
func retryAfter(resp *http.Response) time.Duration {
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(min(secs, int(maxRetryAfter/time.Second))) * time.Second
}

// sleep waits for d, or returns early with ctx's error if the context is done first.
func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
