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
	"testing"
)

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, s Sender)
	}{
		{
			name: "SMTP with credentials",
			env: map[string]string{
				EnvProvider: string(ProviderSMTP), EnvSMTPHost: testSMTPHost, EnvSMTPPort: "2525",
				EnvSMTPTLS: string(TLSImplicit), EnvSMTPUsername: testUser, EnvSMTPPassword: testPassword,
			},
			check: func(t *testing.T, s Sender) {
				want := SMTPConfig{Host: testSMTPHost, Port: 2525, TLS: TLSImplicit, Username: testUser, Password: testPassword}
				if got := s.(*SMTP).cfg; got != want {
					t.Errorf("cfg = %+v, want %+v", got, want)
				}
			},
		},
		{
			name: "SMTP defaults",
			env:  map[string]string{EnvProvider: string(ProviderSMTP), EnvSMTPHost: testSMTPHost},
			check: func(t *testing.T, s Sender) {
				if cfg := s.(*SMTP).cfg; cfg.TLS != TLSStartTLS || cfg.Port != 587 {
					t.Errorf("cfg = %+v, want StartTLS on 587", cfg)
				}
			},
		},
		{
			name: "SES",
			env:  map[string]string{EnvProvider: string(ProviderSES), EnvSESRegion: "eu-west-1"},
			check: func(t *testing.T, s Sender) {
				if _, ok := s.(*SES); !ok {
					t.Errorf("got %T, want *SES", s)
				}
			},
		},
		{name: "provider unset", env: map[string]string{}, wantErr: true},
		{name: "unknown provider", env: map[string]string{EnvProvider: "Webhook"}, wantErr: true},
		{
			name:    "SMTP port not a number",
			env:     map[string]string{EnvProvider: string(ProviderSMTP), EnvSMTPHost: testSMTPHost, EnvSMTPPort: "587a"},
			wantErr: true,
		},
		{name: "SMTP host missing", env: map[string]string{EnvProvider: string(ProviderSMTP)}, wantErr: true},
		{name: "SES region missing", env: map[string]string{EnvProvider: string(ProviderSES)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			s, err := FromEnv(context.Background(), getenv)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, s)
		})
	}
}
