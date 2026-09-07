package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/toolpermission"
)

type permServiceStub struct {
	err error
}

func (s permServiceStub) ListPending(context.Context, string, int) ([]ToolPermissionView, error) {
	return nil, s.err
}

func (s permServiceStub) Decide(context.Context, string, string, string) (ToolPermissionView, error) {
	return ToolPermissionView{}, s.err
}

func (s permServiceStub) DecideWithConfirm(context.Context, string, string, string, bool) (ToolPermissionView, error) {
	return ToolPermissionView{}, s.err
}

func TestPermissionDecideMapsClientErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "not approved", err: fmt.Errorf("issue permission grant: %w", approval.ErrNotApproved), status: http.StatusBadRequest, code: "invalid_request"},
		{name: "grant denied", err: fmt.Errorf("issue permission grant: %w", approval.ErrGrantDenied), status: http.StatusBadRequest, code: "invalid_request"},
		{name: "invalid decide", err: fmt.Errorf("%w: approval has expired", toolpermission.ErrInvalidDecide), status: http.StatusBadRequest, code: "invalid_request"},
		{name: "not pending", err: toolpermission.ErrNotPending, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "not found", err: toolpermission.ErrNotFound, status: http.StatusNotFound, code: "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &API{toolPermissions: permServiceStub{err: tt.err}}
			req := httptest.NewRequest(http.MethodPost, "/v1/permissions/perm-1/decide", strings.NewReader(`{"decision":"allow_similar"}`))
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tt.code {
				t.Fatalf("code = %q, want %q", body.Error.Code, tt.code)
			}
		})
	}
}
