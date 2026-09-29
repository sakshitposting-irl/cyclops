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

// Package email sends the rendered report through SES or SMTP (ADR 0008).
// Delivery settings and credentials come from the report Pod's environment
// (ADR 0014); see FromEnv.
package email

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

const (
	escapeChar = "\n\r"
)

// Message is one email: the rendered report, and who it goes to.
type Message struct {
	// From is the sender, e.g. "cyclops@example.com" or
	// "Cyclops <cyclops@example.com>".
	From string
	// To are the recipients, in the same forms as From. At least one.
	To      []string
	Subject string
	// HTMLBody is the rendered template.
	HTMLBody string
}

// Sender delivers a Message. Implementations: SMTP and SES.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// parsedMessage is a Message whose addresses have been checked.
type parsedMessage struct {
	from *mail.Address
	to   []*mail.Address
	Message
}

// parse checks msg before anything is sent. From, To and Subject end up in
// mail headers, so a CR or LF in them could add headers of their own (e.g. a
// Bcc); they are rejected rather than stripped.
func parse(msg Message) (parsedMessage, error) {
	if strings.ContainsAny(msg.Subject, escapeChar) {
		return parsedMessage{}, errors.New("email: subject contains CR or LF")
	}
	mailFrom, err := parseAddress(msg.From)
	if err != nil {
		return parsedMessage{}, fmt.Errorf("email: invalid From address: %w", err)
	}
	if len(msg.To) == 0 {
		return parsedMessage{}, errors.New("email: To field is empty")
	}
	var mailTo []*mail.Address
	for _, addr := range msg.To {
		parsedAddr, err := parseAddress(addr)
		if err != nil {
			return parsedMessage{}, fmt.Errorf("email: invalid To address %q: %w", addr, err)
		}
		mailTo = append(mailTo, parsedAddr)
	}
	return parsedMessage{
		from:    mailFrom,
		to:      mailTo,
		Message: msg,
	}, nil
}

// parseAddress parses one address, rejecting CR/LF before net/mail sees it.
func parseAddress(s string) (*mail.Address, error) {
	if strings.ContainsAny(s, escapeChar) {
		return nil, errors.New("email: address contains CR or LF")
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return nil, fmt.Errorf("email: invalid address %q: %w", s, err)
	}
	return addr, nil
}
