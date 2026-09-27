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
	"crypto/tls"
	"crypto/x509"
	"errors"
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
	// TODO(user): implement:
	//   1. Host is required.
	//   2. TLS defaults to TLSStartTLS; any value other than the three modes
	//      is an error.
	//   3. Port defaults from TLS (587 / 465 / 25) and must be 1-65535.
	//   4. Username and Password must be set together, or both empty.
	//   5. TLSNone with credentials is an error: they'd be sent in plaintext.
	// Return &SMTP{cfg: cfg, now: time.Now} with the defaults filled in.
	return nil, errors.New("smtp: NewSMTP not implemented")
}

// Send delivers msg in one SMTP session.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	// TODO(user): implement:
	//   1. parse(msg) and buildMIME(pm, s.now()) before dialling, so a bad
	//      message never opens a connection.
	//   2. If ctx has no deadline, give it defaultTimeout.
	//   3. Dial host:port with net.Dialer.DialContext. net/smtp takes no
	//      context, so set ctx's deadline on the conn and use
	//      context.AfterFunc to expire it when ctx is cancelled.
	//   4. TLSImplicit: wrap the conn in tls.Client(conn, s.tlsConfig()) and
	//      HandshakeContext before smtp.NewClient.
	//   5. TLSStartTLS: if c.Extension("STARTTLS") is false, return
	//      ErrStartTLSNotOffered (strict: never fall back to plaintext);
	//      otherwise c.StartTLS(s.tlsConfig()).
	//   6. With credentials: fail if the server doesn't offer AUTH, else
	//      c.Auth(smtp.PlainAuth("", user, password, host)).
	//   7. MAIL FROM, RCPT TO for every recipient (bare addresses), DATA,
	//      write the MIME bytes, Close the writer (the server accepts or
	//      rejects here), then Quit, ignoring its error.
	return errors.New("smtp: Send not implemented")
}

// tlsConfig is used for both implicit TLS and STARTTLS.
func (s *SMTP) tlsConfig() *tls.Config {
	// TODO(user): ServerName: s.cfg.Host, RootCAs: s.rootCAs, MinVersion:
	// tls.VersionTLS12. Never InsecureSkipVerify (ADR 0014).
	return nil
}

// buildMIME renders pm as a complete message: headers, then the HTML body
// as quoted-printable, which keeps lines under SMTP's 998-byte limit however
// long the template's lines are.
func buildMIME(pm parsedMessage, now time.Time) ([]byte, error) {
	// TODO(user): implement. CRLF line endings throughout. Headers:
	//   Date (now, time.RFC1123Z), From and To (mail.Address.String()),
	//   Subject (mime.QEncoding.Encode("utf-8", ...)), Message-ID (messageID),
	//   MIME-Version: 1.0, Content-Type: text/html; charset="UTF-8",
	//   Content-Transfer-Encoding: quoted-printable.
	// Then a blank line and the body through quotedprintable.NewWriter.
	return nil, errors.New("email: buildMIME not implemented")
}

// messageID returns a random Message-ID in the sender's domain. Some spam
// filters penalise mail without one.
func messageID(from string) (string, error) {
	// TODO(user): "<" + 16 random bytes (crypto/rand) as hex + "@" + the
	// part of from after the last "@" + ">". Fall back to "cyclops.invalid"
	// if from has no "@".
	return "", errors.New("email: messageID not implemented")
}
