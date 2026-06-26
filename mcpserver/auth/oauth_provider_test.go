// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// noopLogger is a Logger that discards all output, for use in tests.
type noopLogger struct{}

func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Flush() error         { return nil }

// newMattermostTestServer returns an httptest server that responds to
// GET /api/v4/users/me with the configured status and user. It records the
// Authorization header from the most recent request.
func newMattermostTestServer(t *testing.T, status int, user *model.User) (*httptest.Server, *string) {
	t.Helper()
	var lastAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/users/me" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		lastAuthHeader = r.Header.Get("Authorization")
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(user)
	}))
	t.Cleanup(srv.Close)
	return srv, &lastAuthHeader
}

func TestOAuthProviderValidatesToken(t *testing.T) {
	testUser := &model.User{Id: "user123", Username: "testuser"}

	t.Run("missing token in context is rejected", func(t *testing.T) {
		p := NewOAuthAuthenticationProvider("http://example.com", "", "issuer", noopLogger{})
		if err := p.ValidateAuth(context.Background()); err == nil {
			t.Fatal("expected error when token is missing from context")
		}
	})

	t.Run("valid token returns authenticated client", func(t *testing.T) {
		srv, lastAuthHeader := newMattermostTestServer(t, http.StatusOK, testUser)
		p := NewOAuthAuthenticationProvider(srv.URL, "", "issuer", noopLogger{})

		ctx := context.WithValue(context.Background(), AuthTokenContextKey, "valid-token")
		client, err := p.GetAuthenticatedMattermostClient(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client == nil {
			t.Fatal("expected a non-nil client")
		}
		if want := "token valid-token"; *lastAuthHeader != want {
			t.Fatalf("expected Authorization header %q, got %q", want, *lastAuthHeader)
		}
	})

	t.Run("invalid token is rejected", func(t *testing.T) {
		srv, _ := newMattermostTestServer(t, http.StatusUnauthorized, nil)
		p := NewOAuthAuthenticationProvider(srv.URL, "", "issuer", noopLogger{})

		ctx := context.WithValue(context.Background(), AuthTokenContextKey, "bad-token")
		if _, err := p.GetAuthenticatedMattermostClient(ctx); err == nil {
			t.Fatal("expected error when Mattermost rejects the token")
		}
	})

	t.Run("GetAuthenticatedUser returns the user", func(t *testing.T) {
		srv, _ := newMattermostTestServer(t, http.StatusOK, testUser)
		p := NewOAuthAuthenticationProvider(srv.URL, "", "issuer", noopLogger{})

		ctx := context.WithValue(context.Background(), AuthTokenContextKey, "valid-token")
		user, err := p.GetAuthenticatedUser(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user.Id != testUser.Id {
			t.Fatalf("expected user id %q, got %q", testUser.Id, user.Id)
		}
	})
}
