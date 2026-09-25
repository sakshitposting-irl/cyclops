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

package render

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
)

//go:embed default.html.tmpl
var defaultTemplateText string

// Default is the built-in template, used when a CertReport doesn't point at
// a custom one (ADR 0005). It is parsed once, at startup; a broken default
// template is a bug in cyclops, so it panics rather than returning an error.
var Default = template.Must(Parse(defaultTemplateText))

// Parse parses template text, such as a custom template from a ConfigMap.
// html/template escapes every value by context, so cert-manager's Reason
// text can never inject markup into the email (ADR 0005).
func Parse(text string) (*template.Template, error) {
	return template.New("report").Parse(text)
}

// Render executes t with data and returns the email body. On any error it
// returns "" rather than a partly rendered body, so a broken template never
// sends half an email.
func Render(t *template.Template, data TemplateData) (string, error) {
	// Execute panics on a nil or never-parsed template; return an error
	// instead so the report Job fails cleanly rather than crashing.
	if t == nil || t.Tree == nil {
		return "", errors.New("render: template is nil or has not been parsed")
	}

	// Execute writes output as it goes, so render into a buffer first and
	// only hand the body back once the whole template has succeeded.
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Subject is the email's subject line, set in code rather than by the
// template (ADR 0013).
func Subject(data TemplateData) string {
	n := len(data.Findings)

	if n == 0 {
		return "[cyclops] All certificates healthy"
	}
	if n == 1 {
		return "[cyclops] 1 certificate needs attention"
	}
	return fmt.Sprintf("[cyclops] %d certificates need attention", n)
}
