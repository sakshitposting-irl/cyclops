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
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal SMTP server for tests. It speaks just enough of the
// protocol for net/smtp's client, and records what it was sent.
type fakeSMTP struct {
	ln net.Listener
	// tlsConfig serves a certificate for 127.0.0.1, trusted by roots.
	tlsConfig *tls.Config
	roots     *x509.CertPool

	implicitTLS   bool
	offerStartTLS bool
	offerAuth     bool
	// rejects maps a verb (MAIL, RCPT, AUTH, or dataEnd) to the reply line
	// sent instead of the success reply.
	rejects map[string]string
	// hangOn is a verb (or hangGreeting) after which the server goes silent
	// until the client closes the connection.
	hangOn string

	mu       sync.Mutex
	accepted bool     // a client connected
	commands []string // verbs, in order: EHLO, STARTTLS, AUTH, ...
	auth     string   // the AUTH line's arguments
	mailFrom string
	rcptTo   []string
	data     string
	done     chan struct{}
}

type fakeOption func(*fakeSMTP)

func withImplicitTLS() fakeOption { return func(f *fakeSMTP) { f.implicitTLS = true } }
func withoutStartTLS() fakeOption { return func(f *fakeSMTP) { f.offerStartTLS = false } }
func withoutAuth() fakeOption     { return func(f *fakeSMTP) { f.offerAuth = false } }
func (f *fakeSMTP) port() int     { return f.ln.Addr().(*net.TCPAddr).Port }

// dataEnd is the reject key for the reply to the end of the DATA payload.
const dataEnd = "DATA-END"

// hangGreeting makes the server accept the connection but never greet.
const hangGreeting = "GREETING"

// withReject makes the server answer verb with reply (e.g. "550 no such user").
func withReject(verb, reply string) fakeOption {
	return func(f *fakeSMTP) {
		if f.rejects == nil {
			f.rejects = map[string]string{}
		}
		f.rejects[verb] = reply
	}
}

// withHang makes the server stop answering once it has seen verb.
func withHang(verb string) fakeOption { return func(f *fakeSMTP) { f.hangOn = verb } }

// reply sends the reject for verb if one is configured, else the success line.
func (f *fakeSMTP) reply(tp *textproto.Conn, verb, ok string) {
	if r, found := f.rejects[verb]; found {
		_ = tp.PrintfLine("%s", r)
		return
	}
	_ = tp.PrintfLine("%s", ok)
}

// hang blocks until the client closes the connection.
func hang(conn net.Conn) { _, _ = io.Copy(io.Discard, conn) }

func (f *fakeSMTP) record(verb string) {
	f.mu.Lock()
	f.commands = append(f.commands, verb)
	f.mu.Unlock()
}

// newFakeSMTP starts a server that accepts one session. By default it
// offers STARTTLS and AUTH PLAIN.
func newFakeSMTP(t *testing.T, opts ...fakeOption) *fakeSMTP {
	t.Helper()

	// Borrow httptest's certificate, which is valid for 127.0.0.1.
	tlsSrv := httptest.NewTLSServer(nil)
	cert := tlsSrv.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(tlsSrv.Certificate())
	tlsSrv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{
		ln:            ln,
		tlsConfig:     &tls.Config{Certificates: []tls.Certificate{cert}},
		roots:         roots,
		offerStartTLS: true,
		offerAuth:     true,
		done:          make(chan struct{}),
	}
	for _, o := range opts {
		o(f)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go f.serveOne()
	return f
}

// wasContacted waits for the session to end and reports whether a client
// ever connected. The listener is closed first so a never-used server ends.
func (f *fakeSMTP) wasContacted() bool {
	_ = f.ln.Close()
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accepted
}

// session waits for the session to end and returns the verbs it saw.
func (f *fakeSMTP) session() []string {
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commands
}

func (f *fakeSMTP) serveOne() {
	defer close(f.done)

	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	f.mu.Lock()
	f.accepted = true
	f.mu.Unlock()

	tlsActive := false
	if f.implicitTLS {
		conn = tls.Server(conn, f.tlsConfig)
		tlsActive = true
	}
	if f.hangOn == hangGreeting {
		hang(conn)
		return
	}
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("220 fake ESMTP")

	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, args, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		f.record(verb)
		if f.hangOn == verb {
			hang(conn)
			return
		}

		switch verb {
		case verbEHLO:
			lines := []string{"fake"}
			if f.offerStartTLS && !tlsActive {
				lines = append(lines, "STARTTLS")
			}
			if f.offerAuth {
				lines = append(lines, "AUTH PLAIN")
			}
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				_ = tp.PrintfLine("250%s%s", sep, l)
			}
		case "STARTTLS":
			_ = tp.PrintfLine("220 go ahead")
			tlsConn := tls.Server(conn, f.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			tp = textproto.NewConn(conn)
			tlsActive = true
		case "AUTH":
			f.mu.Lock()
			f.auth = args
			f.mu.Unlock()
			f.reply(tp, verb, "235 ok")
		case "MAIL":
			f.mu.Lock()
			f.mailFrom = args
			f.mu.Unlock()
			f.reply(tp, verb, "250 ok")
		case "RCPT":
			f.mu.Lock()
			f.rcptTo = append(f.rcptTo, args)
			f.mu.Unlock()
			f.reply(tp, verb, "250 ok")
		case "DATA":
			_ = tp.PrintfLine("354 go ahead")
			b, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.data = string(b)
			f.mu.Unlock()
			f.reply(tp, dataEnd, "250 queued")
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("250 ok")
		}
	}
}
