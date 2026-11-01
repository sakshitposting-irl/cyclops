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

package webhooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakshitposting-irl/cyclops/internal/render"
	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// fastBackoff makes the retry waits and the gap between parts milliseconds instead of seconds.
func fastBackoff(t *testing.T) {
	t.Helper()
	oldBackoff, oldGap := backoff, partGap
	backoff, partGap = time.Millisecond, time.Millisecond
	t.Cleanup(func() { backoff, partGap = oldBackoff, oldGap })
}

// replies returns a server that answers each request with the next status in codes (the last one repeats),
// and a counter of the requests it has seen.
func replies(t *testing.T, codes ...int) (url string, calls *atomic.Int32) {
	t.Helper()
	calls = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1))
		w.WriteHeader(codes[min(n, len(codes))-1])
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/services/T0/B0/SECRETSECRET", calls
}

func oneFinding() render.TemplateData {
	return testData(render.Row{Kind: string(report.KindNeverIssued), Namespace: testDev, Name: "x"})
}

func TestSendRetriesTemporaryFailures(t *testing.T) {
	fastBackoff(t)
	url, calls := replies(t, http.StatusInternalServerError, http.StatusTooManyRequests, http.StatusOK)
	s, _ := NewSlack("oncall", url)

	if err := s.Send(context.Background(), oneFinding()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want 3 (500, 429, then 200)", got)
	}
}

func TestSendGivesUpAfterFourTries(t *testing.T) {
	fastBackoff(t)
	url, calls := replies(t, http.StatusServiceUnavailable)
	s, _ := NewSlack("oncall", url)

	err := s.Send(context.Background(), oneFinding())
	if err == nil {
		t.Fatal("want an error")
	}
	if got := calls.Load(); got != maxAttempts {
		t.Errorf("requests = %d, want %d", got, maxAttempts)
	}
	for _, want := range []string{`"oncall"`, "part 1 of 1", "status 503"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestSendDoesNotRetryClientErrors(t *testing.T) {
	fastBackoff(t)
	url, calls := replies(t, http.StatusNotFound)
	s, _ := NewSlack("oncall", url)

	if err := s.Send(context.Background(), oneFinding()); err == nil {
		t.Fatal("want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1: a 404 will not fix itself", got)
	}
}

func TestSendErrorsNeverContainTheURL(t *testing.T) {
	fastBackoff(t)
	url, _ := replies(t, http.StatusInternalServerError)
	dead := httptest.NewServer(nil)
	deadURL := dead.URL + "/services/T0/B0/SECRETSECRET"
	dead.Close() // connection refused

	for name, u := range map[string]string{"status error": url, "network error": deadURL, "bad scheme": "://SECRETSECRET"} {
		t.Run(name, func(t *testing.T) {
			s, _ := NewSlack("oncall", u)
			err := s.Send(context.Background(), oneFinding())
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), "SECRETSECRET") {
				t.Errorf("error leaks the URL: %v", err)
			}
		})
	}
}

func TestSendStopsAtTheFailingPart(t *testing.T) {
	fastBackoff(t)
	// Part 1 succeeds; part 2 always fails with a 400, so there is no retry and part 3 is never sent.
	url, calls := replies(t, http.StatusOK, http.StatusBadRequest)
	s, _ := NewSlack("oncall", url)

	rows := make([]render.Row, findingsPerMessage*2+1)
	for i := range rows {
		rows[i] = render.Row{Kind: string(report.KindNeverIssued), Namespace: testDev, Name: "x"}
	}
	err := s.Send(context.Background(), testData(rows...))
	if err == nil || !strings.Contains(err.Error(), "part 2 of 3") {
		t.Fatalf("want an error naming part 2 of 3, got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestSendStopsWhenContextIsCancelled(t *testing.T) {
	url, calls := replies(t, http.StatusInternalServerError)
	s, _ := NewSlack("oncall", url)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Send(ctx, oneFinding()); err == nil {
		t.Fatal("want an error")
	}
	if got := calls.Load(); got > 1 {
		t.Errorf("requests = %d, want no retries after cancellation", got)
	}
}

func TestRetryAfter(t *testing.T) {
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{"30", 30 * time.Second},
		{"99999", 30 * time.Second}, // capped
		{"99999999999999999999", 0}, // too big to parse
		{"0", 0},
		{"-3", 0},
		{"soon", 0},
	}
	for _, tt := range tests {
		resp := &http.Response{Header: http.Header{"Retry-After": {tt.header}}}
		if got := retryAfter(resp); got != tt.want {
			t.Errorf("Retry-After %q = %v, want %v", tt.header, got, tt.want)
		}
	}
}
