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

package email

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"slices"
	"strings"
	"testing"
	"time"
)

// Test values that appear in several cases.
const (
	testHost     = "127.0.0.1"
	testFrom     = "cyclops@example.com"
	testTo       = "platform@example.com"
	testUser     = "user"
	testPassword = "hunter2"
	testSubject  = "[cyclops] 1 certificate needs attention"
	testBody     = "<p>web in prod — expired</p>"
	testSMTPHost = "smtp.example.com"

	verbEHLO     = "EHLO"
	verbAuth     = "AUTH"
	verbMail     = "MAIL"
	verbRcpt     = "RCPT"
	verbStartTLS = "STARTTLS"
)

var testNow = time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)

func testMessage() Message {
	return Message{From: testFrom, To: []string{testTo}, Subject: testSubject, HTMLBody: testBody}
}

// newTestSMTP builds a sender pointed at f, trusting f's certificate.
func newTestSMTP(t *testing.T, f *fakeSMTP, cfg SMTPConfig) *SMTP {
	t.Helper()
	cfg.Host = testHost
	cfg.Port = f.port()
	s, err := NewSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.rootCAs = f.roots
	s.now = func() time.Time { return testNow }
	return s
}

func TestSMTPStartTLSUpgradesBeforeAuth(t *testing.T) {
	f := newFakeSMTP(t)
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS, Username: testUser, Password: testPassword})

	if err := s.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	want := []string{verbEHLO, verbStartTLS, verbEHLO, verbAuth, verbMail, verbRcpt, "DATA", "QUIT"}
	if got := f.session(); !slices.Equal(got, want) {
		t.Errorf("commands = %v, want %v", got, want)
	}

	mech, resp, _ := strings.Cut(f.auth, " ")
	decoded, err := base64.StdEncoding.DecodeString(resp)
	if mech != "PLAIN" || err != nil || string(decoded) != "\x00"+testUser+"\x00"+testPassword {
		t.Errorf("AUTH %q, want PLAIN with %s/%s", f.auth, testUser, testPassword)
	}
	if f.mailFrom != "FROM:<"+testFrom+">" {
		t.Errorf("MAIL %q", f.mailFrom)
	}
	if !slices.Equal(f.rcptTo, []string{"TO:<" + testTo + ">"}) {
		t.Errorf("RCPT %q", f.rcptTo)
	}
	checkMessage(t, f.data)
}

func TestSMTPStartTLSIsStrict(t *testing.T) {
	f := newFakeSMTP(t, withoutStartTLS())
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS, Username: testUser, Password: testPassword})

	err := s.Send(context.Background(), testMessage())
	if !errors.Is(err, ErrStartTLSNotOffered) {
		t.Fatalf("Send error = %v, want ErrStartTLSNotOffered", err)
	}
	if got := f.session(); slices.Contains(got, verbAuth) || slices.Contains(got, verbMail) {
		t.Errorf("commands = %v; nothing may be sent without TLS", got)
	}
}

func TestSMTPRejectsUntrustedCertificate(t *testing.T) {
	f := newFakeSMTP(t)
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS, Username: testUser, Password: testPassword})
	s.rootCAs = nil // the system pool doesn't trust the fake's certificate

	if err := s.Send(context.Background(), testMessage()); err == nil {
		t.Fatal("Send succeeded against an untrusted certificate")
	}
	if got := f.session(); slices.Contains(got, verbAuth) {
		t.Errorf("commands = %v; credentials sent to an untrusted server", got)
	}
}

func TestSMTPImplicitTLS(t *testing.T) {
	f := newFakeSMTP(t, withImplicitTLS())
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSImplicit, Username: testUser, Password: testPassword})

	if err := s.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := f.session()
	if slices.Contains(got, verbStartTLS) || !slices.Contains(got, verbAuth) {
		t.Errorf("commands = %v, want AUTH and no STARTTLS", got)
	}
	checkMessage(t, f.data)
}

func TestSMTPNoneWithoutCredentials(t *testing.T) {
	f := newFakeSMTP(t, withoutStartTLS(), withoutAuth())
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSNone})

	if err := s.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := f.session(); slices.Contains(got, verbAuth) || slices.Contains(got, verbStartTLS) {
		t.Errorf("commands = %v, want neither AUTH nor STARTTLS", got)
	}
	checkMessage(t, f.data)
}

func TestSMTPCredentialsWithoutServerAuth(t *testing.T) {
	f := newFakeSMTP(t, withoutAuth())
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS, Username: testUser, Password: testPassword})

	if err := s.Send(context.Background(), testMessage()); err == nil {
		t.Fatal("Send succeeded although the server offers no AUTH")
	}
	if got := f.session(); slices.Contains(got, verbMail) {
		t.Errorf("commands = %v; mail sent unauthenticated", got)
	}
}

func TestSMTPCancelledContext(t *testing.T) {
	f := newFakeSMTP(t)
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Send(ctx, testMessage()); err == nil {
		t.Fatal("Send succeeded with a cancelled context")
	}
}

func TestSMTPMultipleRecipients(t *testing.T) {
	f := newFakeSMTP(t)
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS})
	msg := testMessage()
	msg.To = []string{testTo, "second@example.com", "Third Person <third@example.com>"}

	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.session()
	want := []string{"TO:<" + testTo + ">", "TO:<second@example.com>", "TO:<third@example.com>"}
	if !slices.Equal(f.rcptTo, want) {
		t.Errorf("RCPT = %q, want bare addresses %q", f.rcptTo, want)
	}
}

func TestSMTPServerRejects(t *testing.T) {
	tests := []struct {
		name   string
		verb   string
		reply  string
		creds  bool
		wantIn string
	}{
		{name: "after the message body", verb: dataEnd, reply: "554 5.7.1 spam detected", wantIn: "554"},
		{name: "MAIL FROM", verb: "MAIL", reply: "550 5.7.1 sender not allowed", wantIn: "550"},
		{name: "RCPT TO", verb: verbRcpt, reply: "550 5.1.1 user unknown", wantIn: "550"},
		{name: "wrong password", verb: "AUTH", reply: "535 5.7.8 bad credentials", creds: true, wantIn: "535"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSMTP(t, withReject(tt.verb, tt.reply))
			cfg := SMTPConfig{TLS: TLSStartTLS}
			if tt.creds {
				cfg.Username, cfg.Password = testUser, testPassword
			}
			s := newTestSMTP(t, f, cfg)

			err := s.Send(context.Background(), testMessage())
			if err == nil {
				t.Fatal("Send succeeded although the server rejected it")
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error %q should carry the server's %s reply", err, tt.wantIn)
			}
		})
	}
}

func TestSMTPInvalidMessageNeverConnects(t *testing.T) {
	f := newFakeSMTP(t)
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS})
	msg := testMessage()
	msg.Subject = "hi\r\nBcc: attacker@example.com"

	if err := s.Send(context.Background(), msg); err == nil {
		t.Fatal("Send accepted a header-injection subject")
	}
	if f.wasContacted() {
		t.Error("a connection was opened for an invalid message")
	}
}

func TestSMTPHungServerHitsDeadline(t *testing.T) {
	for _, hangOn := range []string{hangGreeting, verbMail} {
		t.Run(hangOn, func(t *testing.T) {
			f := newFakeSMTP(t, withHang(hangOn))
			s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS})
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			start := time.Now()
			err := s.Send(ctx, testMessage())
			if err == nil {
				t.Fatal("Send succeeded against a server that never answers")
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Errorf("Send took %v; the ctx deadline was ignored", elapsed)
			}
		})
	}
}

func TestSMTPCancelMidSession(t *testing.T) {
	f := newFakeSMTP(t, withHang(verbMail))
	s := newTestSMTP(t, f, SMTPConfig{TLS: TLSStartTLS})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)

	start := time.Now()
	err := s.Send(ctx, testMessage())
	if err == nil {
		t.Fatal("Send succeeded although the context was cancelled mid-session")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Send took %v after cancel; it should return promptly", elapsed)
	}
	if !slices.Contains(f.session(), verbMail) {
		t.Error("expected the session to reach MAIL before the cancel")
	}
}

func TestNewSMTP(t *testing.T) {
	tests := []struct {
		name     string
		cfg      SMTPConfig
		wantErr  bool
		wantTLS  TLSMode
		wantPort int
	}{
		{name: "defaults to strict StartTLS on 587", cfg: SMTPConfig{Host: testHost}, wantTLS: TLSStartTLS, wantPort: 587},
		{name: "TLS defaults to 465", cfg: SMTPConfig{Host: testHost, TLS: TLSImplicit}, wantTLS: TLSImplicit, wantPort: 465},
		{name: "None defaults to 25", cfg: SMTPConfig{Host: testHost, TLS: TLSNone}, wantTLS: TLSNone, wantPort: 25},
		{name: "explicit port kept", cfg: SMTPConfig{Host: testHost, Port: 2525}, wantTLS: TLSStartTLS, wantPort: 2525},
		{name: "host required", cfg: SMTPConfig{}, wantErr: true},
		{name: "unknown tls mode", cfg: SMTPConfig{Host: testHost, TLS: "Opportunistic"}, wantErr: true},
		{name: "port out of range", cfg: SMTPConfig{Host: testHost, Port: 70000}, wantErr: true},
		{name: "username without password", cfg: SMTPConfig{Host: testHost, Username: testUser}, wantErr: true},
		{name: "password without username", cfg: SMTPConfig{Host: testHost, Password: testPassword}, wantErr: true},
		{
			name:    "None with credentials",
			cfg:     SMTPConfig{Host: testHost, TLS: TLSNone, Username: testUser, Password: testPassword},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewSMTP(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.cfg.TLS != tt.wantTLS || s.cfg.Port != tt.wantPort {
				t.Errorf("tls/port = %s/%d, want %s/%d", s.cfg.TLS, s.cfg.Port, tt.wantTLS, tt.wantPort)
			}
		})
	}
}

func TestBuildMIME(t *testing.T) {
	msg := testMessage()
	msg.From = "Cyclops <" + testFrom + ">"
	msg.To = []string{testTo, "oncall@example.com"}
	msg.Subject = "Zertifikat läuft ab"
	// Longer than SMTP's 998-byte line limit.
	msg.HTMLBody = "<p>" + strings.Repeat("x", 2000) + "</p>"

	pm, err := parse(msg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildMIME(pm, testNow)
	if err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(string(b), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line of %d bytes exceeds SMTP's limit", len(line))
		}
	}

	m, err := mail.ReadMessage(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if err != nil || subject != msg.Subject {
		t.Errorf("Subject decodes to %q (%v), want %q", subject, err, msg.Subject)
	}
	if got := m.Header.Get("To"); got != "<platform@example.com>, <oncall@example.com>" {
		t.Errorf("To = %q", got)
	}
	if got := m.Header.Get("Date"); got != testNow.Format(time.RFC1123Z) {
		t.Errorf("Date = %q", got)
	}
	if got := m.Header.Get("Message-ID"); !strings.HasSuffix(got, "@example.com>") {
		t.Errorf("Message-ID = %q, want one in the sender's domain", got)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil || string(body) != msg.HTMLBody {
		t.Errorf("body doesn't round-trip (%v)", err)
	}
}

func TestParseRejectsHeaderInjection(t *testing.T) {
	tests := map[string]Message{
		"subject": {From: testFrom, To: []string{testTo}, Subject: "hi\r\nBcc: evil@example.com"},
		"from":    {From: testFrom + "\r\nBcc: evil@example.com", To: []string{testTo}},
		"to":      {From: testFrom, To: []string{testTo + "\nBcc: evil@example.com"}},
	}
	for name, msg := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parse(msg); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestParseRequiresValidAddresses(t *testing.T) {
	tests := map[string]Message{
		"no recipients": {From: testFrom},
		"bad from":      {From: "not an address", To: []string{testTo}},
		"bad to":        {From: testFrom, To: []string{"nope"}},
	}
	for name, msg := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parse(msg); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

// checkMessage checks that data, as received by the fake server, is the
// test message.
func checkMessage(t *testing.T, data string) {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatalf("reading message: %v", err)
	}
	if got := m.Header.Get("Subject"); got != testSubject {
		t.Errorf("Subject = %q, want %q", got, testSubject)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil {
		t.Fatal(err)
	}
	// The dot-reader normalises line endings; compare without them.
	if got := strings.TrimRight(string(body), "\r\n"); got != testBody {
		t.Errorf("body = %q, want %q", got, testBody)
	}
}
