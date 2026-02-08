package dto

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	types "github.com/vibast-solutions/ms-go-notifications/app/types"
)

func TestSendRawRequestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  SendRawRequest
		err  error
	}{
		{name: "missing fields", req: SendRawRequest{}, err: ErrMissingFields},
		{name: "invalid recipient", req: SendRawRequest{RequestID: "1", Recipient: "bad", Subject: "abcd", Content: "long enough"}, err: ErrInvalidRecipient},
		{name: "short subject", req: SendRawRequest{RequestID: "1", Recipient: "a@b.com", Subject: "abc", Content: "long enough"}, err: ErrSubjectTooShort},
		{name: "short content", req: SendRawRequest{RequestID: "1", Recipient: "a@b.com", Subject: "abcd", Content: "short"}, err: ErrContentTooShort},
		{name: "valid", req: SendRawRequest{RequestID: "1", Recipient: "a@b.com", Subject: "abcd", Content: "long enough"}, err: nil},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.req.Validate()
			if err != tc.err {
				t.Fatalf("expected %v, got %v", tc.err, err)
			}
		})
	}
}

func TestFromEchoContextNormalizes(t *testing.T) {
	t.Parallel()

	e := echo.New()
	body := `{"request_id":" 1 ","recipient":" test@example.com ","subject":" subj ","content":" content "}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	dto, err := FromEchoContext(ctx)
	if err != nil {
		t.Fatalf("FromEchoContext returned error: %v", err)
	}
	if dto.RequestID != "1" || dto.Recipient != "test@example.com" || dto.Subject != "subj" || dto.Content != "content" {
		t.Fatalf("unexpected normalization: %+v", dto)
	}
}

func TestFromGRPCNormalizes(t *testing.T) {
	t.Parallel()

	req := &types.SendRawEmailRequest{
		RequestId: " 1 ",
		Recipient: " user@example.com ",
		Subject:   " subject ",
		Content:   " content ",
	}

	dto := FromGRPC(req)
	if dto.RequestID != "1" || dto.Recipient != "user@example.com" || dto.Subject != "subject" || dto.Content != "content" {
		t.Fatalf("unexpected normalization: %+v", dto)
	}
}
