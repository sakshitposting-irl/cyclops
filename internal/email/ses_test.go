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
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// fakeSES records the SendEmail input and returns err.
type fakeSES struct {
	in  *sesv2.SendEmailInput
	err error
}

func (f *fakeSES) SendEmail(_ context.Context, in *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	f.in = in
	return &sesv2.SendEmailOutput{}, f.err
}

func TestSESSend(t *testing.T) {
	fake := &fakeSES{}
	s := &SES{client: fake}

	if err := s.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	in := fake.in
	if got := aws.ToString(in.FromEmailAddress); got != testFrom {
		t.Errorf("From = %q", got)
	}
	if got := in.Destination.ToAddresses; !slices.Equal(got, []string{testTo}) {
		t.Errorf("To = %q", got)
	}
	simple := in.Content.Simple
	if got := aws.ToString(simple.Subject.Data); got != testSubject {
		t.Errorf("Subject = %q", got)
	}
	if got := aws.ToString(simple.Body.Html.Data); got != testBody {
		t.Errorf("Html = %q", got)
	}
	if aws.ToString(simple.Subject.Charset) != charsetUTF8 || aws.ToString(simple.Body.Html.Charset) != charsetUTF8 {
		t.Error("subject and body must declare UTF-8")
	}
	// No images: nothing is attached, and the request is what it was before inline images.
	if simple.Attachments != nil {
		t.Errorf("Attachments = %v, want none", simple.Attachments)
	}
}

func TestSESSendInline(t *testing.T) {
	fake := &fakeSES{}
	s := &SES{client: fake}
	msg := testMessage()
	msg.Inline = []InlineImage{
		{ContentID: "one", ContentType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
		{ContentID: "two", ContentType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
	}

	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	simple := fake.in.Content.Simple
	if len(simple.Attachments) != len(msg.Inline) {
		t.Fatalf("%d attachments, want %d", len(simple.Attachments), len(msg.Inline))
	}
	for i, want := range msg.Inline {
		got := simple.Attachments[i]
		if aws.ToString(got.ContentId) != want.ContentID {
			t.Errorf("attachment %d ContentId = %q, want %q", i, aws.ToString(got.ContentId), want.ContentID)
		}
		if aws.ToString(got.ContentType) != want.ContentType {
			t.Errorf("attachment %d ContentType = %q, want %q", i, aws.ToString(got.ContentType), want.ContentType)
		}
		if got.ContentDisposition != types.AttachmentContentDispositionInline {
			t.Errorf("attachment %d ContentDisposition = %q, want INLINE", i, got.ContentDisposition)
		}
		if got.ContentTransferEncoding != types.AttachmentContentTransferEncodingBase64 {
			t.Errorf("attachment %d ContentTransferEncoding = %q, want BASE64", i, got.ContentTransferEncoding)
		}
		if aws.ToString(got.FileName) == "" {
			t.Errorf("attachment %d has no FileName, which SES requires", i)
		}
		if !slices.Equal(got.RawContent, want.Data) {
			t.Errorf("attachment %d RawContent = %v, want %v", i, got.RawContent, want.Data)
		}
	}
	// The images ride along; the subject and HTML are untouched.
	if got := aws.ToString(simple.Body.Html.Data); got != testBody {
		t.Errorf("Html = %q", got)
	}
}

func TestSESSendError(t *testing.T) {
	sendErr := errors.New("MessageRejected: Email address is not verified")
	s := &SES{client: &fakeSES{err: sendErr}}

	if err := s.Send(context.Background(), testMessage()); !errors.Is(err, sendErr) {
		t.Fatalf("Send error = %v, want it to wrap %v", err, sendErr)
	}
}

func TestSESInvalidMessageNotSent(t *testing.T) {
	fake := &fakeSES{}
	s := &SES{client: fake}

	if err := s.Send(context.Background(), Message{From: testFrom}); err == nil {
		t.Fatal("Send succeeded with no recipients")
	}
	if fake.in != nil {
		t.Error("SendEmail was called for an invalid message")
	}
}

func TestNewSESRequiresRegion(t *testing.T) {
	if _, err := NewSES(context.Background(), SESConfig{}); err == nil {
		t.Fatal("want error, got nil")
	}
}
