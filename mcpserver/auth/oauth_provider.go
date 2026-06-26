// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/mcpserver/logger"
	"github.com/mattermost/mattermost/server/public/model"
)

// OAuthAuthenticationProvider provides OAuth authentication for HTTP transport
// As a resource server, we only need to validate tokens using Mattermost's API
type OAuthAuthenticationProvider struct {
	mmServerURL string // Mattermost server URL for API communication
	issuer      string
	logger      logger.Logger
}

// NewOAuthAuthenticationProvider creates a new OAuth authentication provider for resource server
// Uses internalURL for API communication if provided, otherwise falls back to externalURL
func NewOAuthAuthenticationProvider(externalURL, internalURL, issuer string, logger logger.Logger) *OAuthAuthenticationProvider {
	// Use internal URL for API communication if provided, otherwise fallback to external URL
	mmServerURL := internalURL
	if mmServerURL == "" {
		mmServerURL = externalURL
	}

	return &OAuthAuthenticationProvider{
		mmServerURL: mmServerURL,
		issuer:      issuer,
		logger:      logger,
	}
}

// ValidateAuth validates OAuth authentication from context
func (p *OAuthAuthenticationProvider) ValidateAuth(ctx context.Context) error {
	// Get authenticated client, which handles all validation
	_, err := p.GetAuthenticatedMattermostClient(ctx)
	return err
}

// GetAuthenticatedMattermostClient returns an OAuth-authenticated Mattermost client
func (p *OAuthAuthenticationProvider) GetAuthenticatedMattermostClient(ctx context.Context) (*model.Client4, error) {
	// Get token from context (required for OAuth)
	token, ok := ctx.Value(AuthTokenContextKey).(string)
	if !ok || token == "" {
		return nil, fmt.Errorf("OAuth provider requires validated token in context")
	}

	// Create client and set OAuth token
	client := model.NewAPIv4Client(p.mmServerURL)
	client.SetOAuthToken(token)

	// Validate the token by fetching the current user. As a resource server we
	// rely on Mattermost to introspect the token; a successful GetMe confirms the
	// token is valid and not expired.
	if _, err := p.fetchAuthenticatedUser(ctx, client); err != nil {
		return nil, err
	}

	return client, nil
}

// GetAuthenticatedUser returns the Mattermost user for the OAuth token in context.
func (p *OAuthAuthenticationProvider) GetAuthenticatedUser(ctx context.Context) (*model.User, error) {
	token, ok := ctx.Value(AuthTokenContextKey).(string)
	if !ok || token == "" {
		return nil, fmt.Errorf("OAuth provider requires validated token in context")
	}

	client := model.NewAPIv4Client(p.mmServerURL)
	client.SetOAuthToken(token)

	return p.fetchAuthenticatedUser(ctx, client)
}

func (p *OAuthAuthenticationProvider) fetchAuthenticatedUser(ctx context.Context, client *model.Client4) (*model.User, error) {
	user, _, err := client.GetMe(ctx, "")
	if err != nil {
		p.logger.Error("failed to validate OAuth token",
			"error", err,
			"server_url", p.mmServerURL)
		return nil, fmt.Errorf("invalid OAuth token: %w", err)
	}

	p.logger.Debug("Validated OAuth token for MCP server",
		"user_id", user.Id,
		"username", user.Username)

	return user, nil
}
