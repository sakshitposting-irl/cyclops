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
	"fmt"
	"strings"

	"github.com/sakshitposting-irl/cyclops/internal/render"
	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// Slack's limits, from its documentation (not tested against a real workspace).
const (
	maxBlocks  = 50   // blocks in one message
	maxHeader  = 150  // characters in a header block
	maxSection = 3000 // characters in a section block's text

	// maxDetail caps Reason and IssuerError, which come from the CA and can be long (ADR 0017).
	maxDetail = 300

	// A message carries a header, an info line and then one section per finding.
	findingsPerMessage = maxBlocks - 2
)

// Block is one Slack Block Kit block. Only header and section blocks are used.
type Block struct {
	Type string `json:"type"`
	Text *Text  `json:"text,omitempty"`
}

// Text is the text object inside a block: "plain_text" for headers, "mrkdwn" for sections.
type Text struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func header(s string) Block {
	return Block{Type: "header", Text: &Text{Type: "plain_text", Text: truncate(s, maxHeader)}}
}

// section truncates to maxSection as a backstop. A finding stays well under it unless Reason and
// IssuerError are almost entirely &, < and >, each of which grows when escaped.
func section(s string) Block {
	return Block{Type: "section", Text: &Text{Type: "mrkdwn", Text: truncate(s, maxSection)}}
}

// buildMessages turns the report into one or more Slack messages, in the order they should be
// posted. Findings are split across messages findingsPerMessage at a time, and every message
// repeats the header and the report line so a part read on its own still makes sense.
//
// A welcome report (ADR 0015) is a single short message with no findings.
func buildMessages(data render.TemplateData) []Message {
	title := render.Subject(data)

	if data.Welcome {
		return []Message{{
			Text:   title,
			Blocks: []Block{header(title), section(welcomeText(data))},
		}}
	}

	info := section(fmt.Sprintf("Report `%s` · %s UTC", escape(data.ReportName), data.GeneratedAt.Format("2006-01-02 15:04")))

	parts := max((len(data.Findings)+findingsPerMessage-1)/findingsPerMessage, 1)
	msgs := make([]Message, 0, parts)
	for p := range parts {
		lo := p * findingsPerMessage
		hi := min(lo+findingsPerMessage, len(data.Findings))

		t := title
		if parts > 1 {
			t = fmt.Sprintf("%s (part %d of %d)", title, p+1, parts)
		}
		blocks := []Block{header(t), info}
		for _, r := range data.Findings[lo:hi] {
			blocks = append(blocks, section(findingText(r)))
		}
		msgs = append(msgs, Message{Text: t, Blocks: blocks})
	}
	return msgs
}

// welcomeText is the body of the welcome message: what the report covers and when it runs.
func welcomeText(data render.TemplateData) string {
	scope := "every namespace, or the Ingresses annotated for this report"
	if len(data.Namespaces) > 0 {
		names := make([]string, len(data.Namespaces))
		for i, n := range data.Namespaces {
			names[i] = escape(n)
		}
		scope = "namespaces " + strings.Join(names, ", ")
	}
	text := fmt.Sprintf("Report `%s` is set up. cyclops will post here when a certificate needs attention.", escape(data.ReportName))
	if data.Schedule != "" {
		text += fmt.Sprintf("\nSchedule: `%s` (UTC)", escape(data.Schedule))
	}
	return text + "\nCovers: " + scope
}

// findingText is one certificate: a line with its state, then, when cert-manager gave one, a line
// each for the reason and the issuer error.
func findingText(r render.Row) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s/%s — %s", icon(r.Kind), escape(r.Namespace), escape(r.Name), describe(r))
	for _, detail := range []string{r.Reason, r.IssuerError} {
		if detail = oneLine(detail); detail != "" {
			fmt.Fprintf(&b, "\n     ↳ %s", escape(truncate(detail, maxDetail)))
		}
	}
	return b.String()
}

func icon(kind string) string {
	switch report.Kind(kind) {
	case report.KindExpired:
		return "🔴"
	case report.KindRenewalOverdue:
		return "🟠"
	default:
		return "⚪"
	}
}

func describe(r render.Row) string {
	switch report.Kind(r.Kind) {
	case report.KindExpired:
		return fmt.Sprintf("expired %s ago", plural(-r.DaysLeft, "day"))
	case report.KindRenewalOverdue:
		return "renewal overdue"
	case report.KindNeverIssued:
		return "never issued"
	default:
		return escape(r.Kind)
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// slackEscaper is Slack's required escaping. Escaping < and > also stops a Reason from
// containing <!channel> or <!here> and pinging the channel.
var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escape(s string) string { return slackEscaper.Replace(s) }

// oneLine collapses every run of whitespace, newlines included, into a single space.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// truncate cuts s to at most n characters (not bytes), ending in "…" when it cuts.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
