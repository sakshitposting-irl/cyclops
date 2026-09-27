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
	// TODO(user): implement, returning an error (and nothing else) if:
	//   - Subject contains "\r" or "\n";
	//   - From isn't a valid address (use parseAddress);
	//   - To is empty, or any entry isn't a valid address.
	// On success, return the parsed From and To alongside msg.
	return parsedMessage{}, errors.New("email: parse not implemented")
}

// parseAddress parses one address, rejecting CR/LF before net/mail sees it.
func parseAddress(s string) (*mail.Address, error) {
	// TODO(user): reject s if it contains "\r" or "\n", then parse it with
	// mail.ParseAddress, wrapping the error with the offending address.
	return nil, fmt.Errorf("email: parseAddress(%q) not implemented", s)
}
