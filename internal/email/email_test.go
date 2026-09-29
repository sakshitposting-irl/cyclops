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
	"strings"
	"testing"
)

const (
	testOncall    = "oncall@example.com"
	testNamedFrom = "Cyclops <" + testFrom + ">"
	testBadAddr   = "nope"
)

func TestParseAddress(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantErr  bool
		wantName string
		wantAddr string
	}{
		{name: "bare address", in: testFrom, wantAddr: testFrom},
		{name: "angle brackets", in: "<" + testFrom + ">", wantAddr: testFrom},
		{name: "display name", in: testNamedFrom, wantName: "Cyclops", wantAddr: testFrom},
		{name: "quoted display name", in: `"Platform, Team" <` + testTo + ">", wantName: "Platform, Team", wantAddr: testTo},
		{name: "empty", in: "", wantErr: true},
		{name: "no domain", in: "cyclops", wantErr: true},
		{name: "spaces, no @", in: "cyclops at example dot com", wantErr: true},
		{name: "two addresses", in: testFrom + ", " + testTo, wantErr: true},
		// Each line break on its own, not only the CRLF pair: some servers
		// treat a bare LF (or CR) as a line ending too.
		{name: "CRLF", in: testFrom + "\r\nBcc: evil@example.com", wantErr: true},
		{name: "bare LF", in: testFrom + "\nBcc: evil@example.com", wantErr: true},
		{name: "bare CR", in: testFrom + "\rBcc: evil@example.com", wantErr: true},
		{name: "escape tab", in: testFrom + "\tBcc: evil@example.com", wantErr: true},
		{name: "line break in display name", in: "Cyc\r\nlops <" + testFrom + ">", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := parseAddress(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAddress(%q) = %v, want error", tt.in, addr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAddress(%q) error = %v", tt.in, err)
			}
			if addr.Name != tt.wantName || addr.Address != tt.wantAddr {
				t.Errorf("parseAddress(%q) = %q <%s>, want %q <%s>", tt.in, addr.Name, addr.Address, tt.wantName, tt.wantAddr)
			}
		})
	}
}

func TestParseAddressErrorNamesInput(t *testing.T) {
	_, err := parseAddress(testBadAddr)
	if err == nil || !strings.Contains(err.Error(), `"`+testBadAddr+`"`) {
		t.Errorf("error = %v, want it to quote the offending address", err)
	}
}

func TestParseValid(t *testing.T) {
	msg := Message{
		From:     testNamedFrom,
		To:       []string{testTo, "On-call <" + testOncall + ">"},
		Subject:  testSubject,
		HTMLBody: testBody,
	}

	pm, err := parse(msg)
	if err != nil {
		t.Fatalf("parse() error = %v", err)
	}

	if pm.from == nil || pm.from.Name != "Cyclops" || pm.from.Address != testFrom {
		t.Errorf("from = %v, want Cyclops <%s>", pm.from, testFrom)
	}
	if len(pm.to) != 2 || pm.to[0].Address != testTo || pm.to[1].Address != testOncall || pm.to[1].Name != "On-call" {
		t.Errorf("to = %v, want %s and On-call <%s>, in order", pm.to, testTo, testOncall)
	}
	// The original message is carried through untouched.
	if pm.Subject != testSubject || pm.HTMLBody != testBody || pm.From != msg.From {
		t.Errorf("message fields changed: %+v", pm.Message)
	}
}

func TestParseSubject(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		wantErr bool
	}{
		{name: "plain", subject: testSubject},
		{name: "empty", subject: ""},
		{name: "non-ASCII", subject: "Zertifikat läuft ab — 2 Einträge"},
		// A tab can't start a new header, so it's harmless; only CR and LF
		// are rejected.
		{name: "tab", subject: "a\tb"},
		{name: "CRLF", subject: "hi\r\nBcc: evil@example.com", wantErr: true},
		{name: "bare LF", subject: "hi\nBcc: evil@example.com", wantErr: true},
		{name: "bare CR", subject: "hi\rBcc: evil@example.com", wantErr: true},
		{name: "trailing LF", subject: testSubject + "\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := testMessage()
			msg.Subject = tt.subject
			_, err := parse(msg)
			if tt.wantErr && err == nil {
				t.Fatalf("parse() accepted subject %q", tt.subject)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("parse() rejected subject %q: %v", tt.subject, err)
			}
		})
	}
}

func TestParseRecipients(t *testing.T) {
	tests := []struct {
		name    string
		to      []string
		wantErr bool
	}{
		{name: "one", to: []string{testTo}},
		{name: "several", to: []string{testTo, testOncall}},
		{name: "nil", to: nil, wantErr: true},
		{name: "empty slice", to: []string{}, wantErr: true},
		{name: "empty entry", to: []string{testTo, ""}, wantErr: true},
		// One bad address fails the whole message rather than being skipped:
		// silently dropping a recipient would hide the misconfiguration.
		{name: "one bad among good", to: []string{testTo, testBadAddr, testOncall}, wantErr: true},
		{name: "injection in a later entry", to: []string{testTo, testOncall + "\nBcc: evil@example.com"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := testMessage()
			msg.To = tt.to
			pm, err := parse(msg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parse() accepted To %q", tt.to)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse() error = %v", err)
			}
			if len(pm.to) != len(tt.to) {
				t.Errorf("parsed %d recipients, want %d", len(pm.to), len(tt.to))
			}
		})
	}
}

func TestParseFrom(t *testing.T) {
	for _, from := range []string{"", testBadAddr, testFrom + "\r\nBcc: evil@example.com"} {
		msg := testMessage()
		msg.From = from
		if _, err := parse(msg); err == nil {
			t.Errorf("parse() accepted From %q", from)
		}
	}
}
