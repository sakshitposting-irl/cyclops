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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// TLSMode is how the SMTP connection is encrypted (ADR 0014).
type TLSMode string

const (
	// TLSStartTLS connects in plaintext and upgrades with STARTTLS before
	// anything else is sent. Strict: if the server doesn't offer STARTTLS,
	// the send fails instead of continuing in plaintext.
	TLSStartTLS TLSMode = "StartTLS"
	// TLSImplicit is encrypted from the first byte (usually port 465).
	TLSImplicit TLSMode = "TLS"
	// TLSNone never encrypts. Only for unauthenticated relays inside the
	// cluster; it can't be combined with credentials.
	TLSNone TLSMode = "None"
)

// ErrStartTLSNotOffered is returned when TLSStartTLS is configured but the
// server doesn't announce STARTTLS: a misconfigured server, or someone
// stripping the announcement. Nothing, least of all the password, has been
// sent when it's returned.
var ErrStartTLSNotOffered = errors.New("smtp: server does not offer STARTTLS")

// defaultTimeout bounds a whole send when ctx has no deadline, so a server
// that stops answering can't hang the report Job forever.
const defaultTimeout = 30 * time.Second

// SMTPConfig is the SMTP half of the install-level delivery settings.
type SMTPConfig struct {
	Host string
	// Port defaults from TLS: 587 for StartTLS, 465 for TLS, 25 for None.
	Port int
	// TLS defaults to TLSStartTLS.
	TLS TLSMode
	// Username and Password are both set, or both empty for no AUTH.
	Username string
	Password string
}

// SMTP sends through an SMTP server.
type SMTP struct {
	cfg SMTPConfig
	// rootCAs is the pool server certificates are checked against; nil
	// means the system pool. Only tests set it. There is deliberately no
	// way to skip verification (ADR 0014).
	rootCAs *x509.CertPool
	now     func() time.Time
}

var _ Sender = (*SMTP)(nil)

// NewSMTP checks cfg, fills in defaults, and returns a sender. Nothing is
// dialled until Send.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	// if SMTP Host eg( smtp.example.com) is empty, return an error
	if cfg.Host == "" {
		return nil, errors.New("smtp: Host is required")
	}

	/*
		NOTES;
		TLSStartTLS: Connects in plaintext and upgrades with STARTTLS before anything else is sent. If the server doesn't offer STARTTLS, the send fails instead of continuing in plaintext.
		TLSImplicit: Encrypted from the first byte (usually port 465).
		TLSNone: Never encrypts. Only for unauthenticated relays inside the cluster; it can't be combined with credentials.
	*/

	// if TLS is empty, set it to TLSStartTLS. If it's not one of the three valid modes, return an error
	if cfg.TLS == "" {
		cfg.TLS = TLSStartTLS
	}
	if cfg.TLS != TLSStartTLS && cfg.TLS != TLSImplicit && cfg.TLS != TLSNone {
		return nil, errors.New("smtp: TLS must be one of StartTLS, TLS, None")
	}
	// if Port is 0 (not specified), set it to the default for the TLS mode. If it's not in the range 1-65535, return an error
	if cfg.Port == 0 {
		switch cfg.TLS {
		case TLSStartTLS:
			cfg.Port = 587
		case TLSImplicit:
			cfg.Port = 465
		case TLSNone:
			cfg.Port = 25
		}
	} else if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("smtp: Port must be 1-65535")
	}

	// check that Username and Password are either both set or both empty. If one is set and the other is not, return an error. Also, if TLS is TLSNone and credentials are provided, return an error
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("smtp: Username and Password must be set together or both empty")
	}
	if cfg.TLS == TLSNone && cfg.Username != "" {
		return nil, errors.New("smtp: TLSNone cannot be used with credentials")
	}

	return &SMTP{cfg: cfg, now: time.Now}, nil
}

// Send delivers msg in one SMTP session.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	// returning if message or MIME is invalid, before opening a connection to the SMTP server

	pm, err := parse(msg)
	if err != nil {
		return err
	}
	mimeBytes, err := buildMIME(pm, s.now())
	if err != nil {
		return err
	}

	// if ctx has no deadline, give it defaultTimeout so that a server that stops answering can't hang the report Job forever
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	// using net.Dialer to establish a connection to the SMTP server with the provided context, not using net.Dial because it does not support context cancellation or timeouts
	dialer := &net.Dialer{}
	dialer.Timeout = defaultTimeout

	// using DialContext to establish a connection to the SMTP server with the provided context, not using Dial because it does not support context cancellation or timeouts
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return err
	}
	// Close the connection when we're done, even if we return early due to an error.
	defer func() { _ = conn.Close() }()

	// if ctx has a deadline, set it on the connection so that the SMTP client respects it. If setting the deadline fails, return the error
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	// use context.AfterFunc to expire the connection's deadline when the context is cancelled, so that the SMTP client respects it. This is necessary because net/smtp does not support context cancellation or timeouts
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	// defer the stop so that it is called when the function returns, even if we return early due to an error
	defer stop()

	// if TLS is implicit, wrap the connection in a TLS client and perform the handshake before creating the SMTP client. If the handshake fails, return the error

	// NOTES: Refer TLS Handshake: https://docs.tlsref.org/server-side-tls.html
	if s.cfg.TLS == TLSImplicit {
		// wrap the TCP connection in a TLS client using the provided TLS configuration, and perform the handshake before creating the SMTP client. If the handshake fails, return the error
		tlsConn := tls.Client(conn, s.tlsConfig())
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = tlsConn
	}

	// finally when the TLS handshake is done, create the SMTP client using the connection and the host. If creating the client fails, return the error. Defer quitting the client so that it is called when the function returns, even if we return early due to an error
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return err
	}
	defer func() { _ = c.Quit() }()

	// only STARTTLS mode upgrades here: implicit TLS is already encrypted and None never is
	if s.cfg.TLS == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return ErrStartTLSNotOffered
		}
		if err := c.StartTLS(s.tlsConfig()); err != nil {
			return err
		}
	}
	// if the server offers AUTH, authenticate using the provided username and password. If authentication fails, return the error
	if s.cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server does not offer AUTH")
		}
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return err
		}
	}
	// send the MAIL FROM command with the sender's email address. If it fails, return the error
	if err := c.Mail(pm.from.Address); err != nil {
		return err
	}
	for _, to := range pm.to {
		// send the RCPT TO command for each recipient's email address. If it fails, return the error
		if err := c.Rcpt(to.Address); err != nil {
			return err
		}
	}
	// client.Data() returns a writer to which the message data can be written. If it fails, return the error
	w, err := c.Data()
	if err != nil {
		return err
	}
	// write the MIME bytes to the SMTP client. If it fails, return the error
	if _, err := w.Write(mimeBytes); err != nil {
		return err
	}
	// close the writer to signal that we're done sending the message
	if err := w.Close(); err != nil {
		return err
	}
	return nil
}

// tlsConfig is used for both implicit TLS and STARTTLS.
func (s *SMTP) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName: s.cfg.Host,
		RootCAs:    s.rootCAs,
		MinVersion: tls.VersionTLS12,
	}
}

// buildMIME renders pm as a complete message: headers, then the HTML body
// as quoted-printable, which keeps lines under SMTP's 998-byte limit however
// long the template's lines are.
func buildMIME(pm parsedMessage, now time.Time) ([]byte, error) {
	mimeBuffer := &bytes.Buffer{}

	mimeBuffer.WriteString("Date: ")
	mimeBuffer.WriteString(now.Format(time.RFC1123Z))
	mimeBuffer.WriteString("\r\n")

	mimeBuffer.WriteString("From: ")
	mimeBuffer.WriteString(pm.from.String())
	mimeBuffer.WriteString("\r\n")

	mimeBuffer.WriteString("To: ")
	for i, recipient := range pm.to {
		if i > 0 {
			mimeBuffer.WriteString(", ")
		}
		mimeBuffer.WriteString(recipient.String())
	}
	mimeBuffer.WriteString("\r\n")

	mimeBuffer.WriteString("Subject: ")
	mimeBuffer.WriteString(mime.QEncoding.Encode("utf-8", pm.Subject))
	mimeBuffer.WriteString("\r\n")

	messageID, err := messageID(pm.from.Address)
	if err != nil {
		return nil, err
	}
	mimeBuffer.WriteString("Message-ID: ")
	mimeBuffer.WriteString(messageID)
	mimeBuffer.WriteString("\r\n")

	mimeBuffer.WriteString("MIME-Version: 1.0\r\n")
	mimeBuffer.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	mimeBuffer.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	mimeBuffer.WriteString("\r\n")

	htmlWriter := quotedprintable.NewWriter(mimeBuffer)
	_, err = htmlWriter.Write([]byte(pm.HTMLBody))
	if err != nil {
		return nil, err
	}

	err = htmlWriter.Close()
	if err != nil {
		return nil, err
	}

	return mimeBuffer.Bytes(), nil
}

// messageID returns a random Message-ID in the sender's domain. Some spam
// filters penalise mail without one.
func messageID(from string) (string, error) {
	var b strings.Builder
	b.WriteString("<")
	// Generate 16 random bytes
	randomBytes := make([]byte, 16)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", err
	}
	// Convert to hex
	b.WriteString(hex.EncodeToString(randomBytes))
	b.WriteString("@")

	atIndex := strings.LastIndex(from, "@")
	if atIndex == -1 || atIndex == len(from)-1 {
		b.WriteString("cyclops.invalid")
	} else {
		b.WriteString(from[atIndex+1:])
	}
	b.WriteString(">")

	return b.String(), nil
}
