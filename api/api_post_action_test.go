// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	plugini18n "github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type approvalTestToolProvider struct {
	tools []llm.Tool
}

func (p *approvalTestToolProvider) GetTools(_ *bots.Bot) []llm.Tool {
	return p.tools
}

type approvalTestContextConfigProvider struct{}

func (p *approvalTestContextConfigProvider) GetEnableLLMTrace() bool {
	return false
}

func (p *approvalTestContextConfigProvider) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

type approvalTestConversationConfig struct{}

func (c *approvalTestConversationConfig) EnableChannelMentionToolCalling() bool {
	return true
}

func (c *approvalTestConversationConfig) AllowNativeWebSearchInChannels() bool {
	return false
}

func (c *approvalTestConversationConfig) MCP() mcp.Config {
	return mcp.Config{}
}

type falseLicenseChecker struct{}

func (f *falseLicenseChecker) IsBasicsLicensed() bool { return false }

type approvalToolArgs struct {
	Value string `json:"value"`
}

type approvalFakeMMClient struct {
	users        map[string]*model.User
	kv           map[string]any
	updatedPosts []*model.Post
	kvDeletes    []string
	posts        map[string]*model.Post
	channels     map[string]*model.Channel
	postThreads  map[string]*model.PostList
}

func (c *approvalFakeMMClient) GetUser(userID string) (*model.User, error) {
	user, ok := c.users[userID]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

func (c *approvalFakeMMClient) GetPostThread(postID string) (*model.PostList, error) {
	postList, ok := c.postThreads[postID]
	if !ok {
		return nil, errors.New("thread not found")
	}
	return postList, nil
}

func (c *approvalFakeMMClient) CreatePost(*model.Post) error {
	return errors.New("not implemented")
}

func (c *approvalFakeMMClient) UpdatePost(post *model.Post) error {
	c.updatedPosts = append(c.updatedPosts, post.Clone())
	return nil
}

func (c *approvalFakeMMClient) KVGet(key string, value any) error {
	stored, ok := c.kv[key]
	if !ok {
		return errors.New("not found")
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (c *approvalFakeMMClient) KVSet(key string, value any) error {
	if c.kv == nil {
		c.kv = map[string]any{}
	}
	c.kv[key] = value
	return nil
}

func (c *approvalFakeMMClient) KVSetWithExpiry(key string, value any, _ time.Duration) error {
	return c.KVSet(key, value)
}

func (c *approvalFakeMMClient) KVDelete(key string) error {
	delete(c.kv, key)
	c.kvDeletes = append(c.kvDeletes, key)
	return nil
}

func (c *approvalFakeMMClient) AddReaction(*model.Reaction) error {
	return errors.New("not implemented")
}

func (c *approvalFakeMMClient) GetPost(postID string) (*model.Post, error) {
	post, ok := c.posts[postID]
	if !ok {
		return nil, errors.New("post not found")
	}
	return post, nil
}

func (c *approvalFakeMMClient) GetPostsSince(string, int64) (*model.PostList, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) GetPostsBefore(string, string, int, int) (*model.PostList, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) DM(string, string, *model.Post) error {
	return errors.New("not implemented")
}

func (c *approvalFakeMMClient) GetTeam(string) (*model.Team, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) GetChannel(channelID string) (*model.Channel, error) {
	channel, ok := c.channels[channelID]
	if !ok {
		return nil, errors.New("channel not found")
	}
	return channel, nil
}

func (c *approvalFakeMMClient) GetDirectChannel(string, string) (*model.Channel, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) PublishWebSocketEvent(string, map[string]any, *model.WebsocketBroadcast) {
}

func (c *approvalFakeMMClient) GetConfig() *model.Config {
	return &model.Config{}
}

func (c *approvalFakeMMClient) LogError(string, ...any) {}

func (c *approvalFakeMMClient) LogWarn(string, ...any) {}

func (c *approvalFakeMMClient) GetUserByUsername(string) (*model.User, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) GetUserStatus(string) (*model.Status, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) HasPermissionTo(string, *model.Permission) bool {
	return true
}

func (c *approvalFakeMMClient) GetPluginStatus(string) (*model.PluginStatus, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) PluginHTTP(*http.Request) *http.Response {
	return nil
}

func (c *approvalFakeMMClient) LogDebug(string, ...any) {}

func (c *approvalFakeMMClient) GetChannelByName(string, string, bool) (*model.Channel, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) HasPermissionToChannel(string, string, *model.Permission) bool {
	return true
}

func (c *approvalFakeMMClient) GetFileInfo(string) (*model.FileInfo, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) GetFile(string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (c *approvalFakeMMClient) SendEphemeralPost(string, *model.Post) {}

func (c *approvalFakeMMClient) DeletePost(string) error { return nil }

func setupToolApprovalTestEnvironment(t *testing.T) (*TestEnvironment, *approvalFakeMMClient) {
	t.Helper()

	e := SetupTestEnvironment(t)
	e.api.i18nBundle = plugini18n.Init()
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.config.enableChannelMentionToolCalling = true
	e.setupTestBot(llm.BotConfig{Name: "ai", DisplayName: "AI"})
	e.mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
	siteName := "Mattermost"
	siteURL := "https://example.com"
	e.mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}, ServiceSettings: model.ServiceSettings{SiteURL: &siteURL}}).Maybe()
	e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil).Maybe()

	tool := llm.Tool{
		Name:        "test_tool",
		Description: "test tool",
		Schema:      llm.NewJSONSchemaFromStruct[approvalToolArgs](),
		Resolver: func(_ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
			var parsed approvalToolArgs
			if err := args(&parsed); err != nil {
				return "", err
			}
			return "ok:" + parsed.Value, nil
		},
	}

	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&approvalTestToolProvider{tools: []llm.Tool{tool}},
		nil,
		&approvalTestContextConfigProvider{},
	)

	mmClient := &approvalFakeMMClient{
		users: map[string]*model.User{
			testUserID:      {Id: testUserID, Username: "requester", Locale: "en"},
			testOtherUserID: {Id: testOtherUserID, Username: "other", Locale: "en"},
			testBotUserID:   {Id: testBotUserID, Username: "ai", Locale: "en"},
		},
		kv: map[string]any{},
	}

	e.api.conversationsService = conversations.New(
		nil,
		mmClient,
		nil,
		e.api.contextBuilder,
		e.bots,
		nil,
		enterprise.NewLicenseChecker(e.client),
		e.api.i18nBundle,
		nil,
		&approvalTestConversationConfig{},
	)

	return e, mmClient
}

func newToolApprovalPost(toolProp string) *model.Post {
	post := &model.Post{
		Id:        "postid",
		UserId:    testBotUserID,
		ChannelId: testChannelID,
		Props: model.StringInterface{
			streaming.LLMRequesterUserID:      testUserID,
			streaming.AllowToolsInChannelProp: "true",
		},
	}
	if toolProp != "" {
		post.AddProp(streaming.ToolCallProp, toolProp)
	}
	return post
}

func makeToolApprovalRequest(t *testing.T, userID string, context map[string]any) *http.Request {
	t.Helper()

	// Simulate the new architecture: the button is on a separate approval post
	// (approval-postid) which routes back to the original bot post via original_post_id.
	merged := make(map[string]any, len(context)+1)
	maps.Copy(merged, context)
	merged["original_post_id"] = "postid"

	body, err := json.Marshal(model.PostActionIntegrationRequest{
		UserId:  userID,
		PostId:  "approval-postid",
		Context: merged,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/actions/tool_approval", strings.NewReader(string(body)))
	req.Header.Set("Mattermost-User-Id", userID)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func parseToolApprovalResponse(t *testing.T, recorder *httptest.ResponseRecorder) model.PostActionIntegrationResponse {
	t.Helper()
	var resp model.PostActionIntegrationResponse
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	return resp
}

func TestHandleToolApprovalAction(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	callToolIDs := []string{"tool-1", "tool-2"}
	callToolCalls := []llm.ToolCall{
		{ID: "tool-1", Name: "test_tool", Arguments: json.RawMessage(`{"value":"alpha"}`), Status: llm.ToolCallStatusPending},
		{ID: "tool-2", Name: "test_tool", Arguments: json.RawMessage(`{"value":"beta"}`), Status: llm.ToolCallStatusPending},
	}
	callToolCallsJSON, err := json.Marshal(streaming.RedactToolCalls(callToolCalls))
	require.NoError(t, err)

	resultToolIDs := []string{"result-1", "result-2"}
	resultToolCalls := []llm.ToolCall{
		{ID: "result-1", Name: "test_tool", Result: "boom", Status: llm.ToolCallStatusError},
		{ID: "result-2", Name: "test_tool", Result: "still boom", Status: llm.ToolCallStatusError},
	}

	tests := []struct {
		name                 string
		requestUserID        string
		context              map[string]any
		post                 *model.Post
		configureEnvironment func(*TestEnvironment, *approvalFakeMMClient, *model.Post)
		expectedStatus       int
		expectedText         string
		assertions           func(*testing.T, *approvalFakeMMClient, *model.Post, *model.Post)
	}{
		{
			name:          "valid accept_all request returns ephemeral success",
			requestUserID: testUserID,
			context:       map[string]any{"stage": "call", "action": "accept_all", "tool_ids": callToolIDs},
			post:          newToolApprovalPost(string(callToolCallsJSON)),
			configureEnvironment: func(e *TestEnvironment, mmClient *approvalFakeMMClient, post *model.Post) {
				toolCallKVKey := streaming.ToolCallPrivateKVKey(post.Id, testUserID)
				mmClient.kv[toolCallKVKey] = callToolCalls
				e.mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
				siteName := "Mattermost"
				siteURL := "https://example.com"
				e.mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}, ServiceSettings: model.ServiceSettings{SiteURL: &siteURL}}).Maybe()
				e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil).Maybe()
			},
			expectedStatus: http.StatusOK,
			expectedText:   "Tools approved. Processing...",
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, post *model.Post, clearedPost *model.Post) {
				require.Nil(t, clearedPost.GetProp(model.PostPropsAttachments))
				require.Len(t, mmClient.updatedPosts, 1)
				stored := mmClient.kv[streaming.ToolResultPrivateKVKey(post.Id, testUserID)]
				resultCalls, ok := stored.([]llm.ToolCall)
				require.True(t, ok)
				require.Len(t, resultCalls, 2)
				require.Equal(t, llm.ToolCallStatusSuccess, resultCalls[0].Status)
				require.Equal(t, "ok:alpha", resultCalls[0].Result)
				require.Equal(t, llm.ToolCallStatusSuccess, resultCalls[1].Status)
				require.Equal(t, "ok:beta", resultCalls[1].Result)
			},
		},
		{
			name:          "valid reject_all request returns ephemeral success",
			requestUserID: testUserID,
			context:       map[string]any{"stage": "call", "action": "reject_all", "tool_ids": callToolIDs},
			post:          newToolApprovalPost(string(callToolCallsJSON)),
			configureEnvironment: func(e *TestEnvironment, mmClient *approvalFakeMMClient, post *model.Post) {
				toolCallKVKey := streaming.ToolCallPrivateKVKey(post.Id, testUserID)
				mmClient.kv[toolCallKVKey] = callToolCalls
				e.mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
				siteName := "Mattermost"
				siteURL := "https://example.com"
				e.mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}, ServiceSettings: model.ServiceSettings{SiteURL: &siteURL}}).Maybe()
				e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil).Maybe()
			},
			expectedStatus: http.StatusOK,
			expectedText:   "Tools rejected.",
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, post *model.Post, clearedPost *model.Post) {
				require.Nil(t, clearedPost.GetProp(model.PostPropsAttachments))
				require.Len(t, mmClient.updatedPosts, 1)
				_, found := mmClient.kv[streaming.ToolResultPrivateKVKey(post.Id, testUserID)]
				require.False(t, found)

				toolCallProp, ok := post.GetProp(streaming.ToolCallProp).(string)
				require.True(t, ok)
				var stored []llm.ToolCall
				require.NoError(t, json.Unmarshal([]byte(toolCallProp), &stored))
				require.Len(t, stored, 2)
				require.Equal(t, llm.ToolCallStatusRejected, stored[0].Status)
				require.Equal(t, llm.ToolCallStatusRejected, stored[1].Status)
			},
		},
		{
			name:          "valid share_results request returns ephemeral success",
			requestUserID: testUserID,
			context:       map[string]any{"stage": "result", "action": "share_results", "tool_ids": resultToolIDs},
			post: func() *model.Post {
				post := newToolApprovalPost(string(callToolCallsJSON))
				post.AddProp(streaming.PendingToolResultProp, "true")
				return post
			}(),
			configureEnvironment: func(e *TestEnvironment, mmClient *approvalFakeMMClient, post *model.Post) {
				mmClient.kv[streaming.ToolResultPrivateKVKey(post.Id, testUserID)] = resultToolCalls
				mmClient.kv[streaming.ToolCallPrivateKVKey(post.Id, testUserID)] = callToolCalls
				e.mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
			},
			expectedStatus: http.StatusOK,
			expectedText:   "Results shared.",
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, post *model.Post, clearedPost *model.Post) {
				require.Nil(t, clearedPost.GetProp(model.PostPropsAttachments))
				require.Len(t, mmClient.updatedPosts, 1)
				require.Nil(t, post.GetProp(streaming.PendingToolResultProp))
				require.Contains(t, mmClient.kvDeletes, streaming.ToolResultPrivateKVKey(post.Id, testUserID))
				require.Contains(t, mmClient.kvDeletes, streaming.ToolCallPrivateKVKey(post.Id, testUserID))
			},
		},
		{
			name:          "valid keep_private request returns ephemeral success",
			requestUserID: testUserID,
			context:       map[string]any{"stage": "result", "action": "keep_private", "tool_ids": resultToolIDs},
			post: func() *model.Post {
				post := newToolApprovalPost(string(callToolCallsJSON))
				post.AddProp(streaming.PendingToolResultProp, "true")
				return post
			}(),
			configureEnvironment: func(e *TestEnvironment, mmClient *approvalFakeMMClient, post *model.Post) {
				mmClient.kv[streaming.ToolResultPrivateKVKey(post.Id, testUserID)] = resultToolCalls
				mmClient.kv[streaming.ToolCallPrivateKVKey(post.Id, testUserID)] = callToolCalls
				e.mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
			},
			expectedStatus: http.StatusOK,
			expectedText:   "Results kept private.",
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, post *model.Post, clearedPost *model.Post) {
				require.Nil(t, clearedPost.GetProp(model.PostPropsAttachments))
				require.Len(t, mmClient.updatedPosts, 1)
				require.Nil(t, post.GetProp(streaming.PendingToolResultProp))
				toolCallProp, ok := post.GetProp(streaming.ToolCallProp).(string)
				require.True(t, ok)
				var stored []llm.ToolCall
				require.NoError(t, json.Unmarshal([]byte(toolCallProp), &stored))
				require.Len(t, stored, 2)
				require.Equal(t, llm.ToolCallStatusRejected, stored[0].Status)
				require.Equal(t, llm.ToolCallStatusRejected, stored[1].Status)
			},
		},
		{
			name:           "missing post returns ephemeral error",
			requestUserID:  testUserID,
			context:        map[string]any{"stage": "call", "action": "accept_all", "tool_ids": callToolIDs},
			expectedStatus: http.StatusOK,
			expectedText:   "This tool approval is no longer available.",
			configureEnvironment: func(e *TestEnvironment, _ *approvalFakeMMClient, _ *model.Post) {
				e.mockAPI.ExpectedCalls = nil
				e.mockAPI.Calls = nil
				e.mockAPI.On("GetPost", "postid").Return(nil, nil)
				e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Locale: "en"}, nil)
			},
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, _ *model.Post, _ *model.Post) {
				require.Empty(t, mmClient.updatedPosts)
			},
		},
		{
			name:           "wrong user returns ephemeral error",
			requestUserID:  testOtherUserID,
			context:        map[string]any{"stage": "call", "action": "accept_all", "tool_ids": callToolIDs},
			post:           newToolApprovalPost(string(callToolCallsJSON)),
			expectedStatus: http.StatusOK,
			expectedText:   "Only the person who triggered this can approve.",
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, _ *model.Post, _ *model.Post) {
				require.Empty(t, mmClient.updatedPosts)
			},
		},
		{
			name:           "already processed returns ephemeral error",
			requestUserID:  testUserID,
			context:        map[string]any{"stage": "call", "action": "accept_all", "tool_ids": callToolIDs},
			post:           newToolApprovalPost(""),
			expectedStatus: http.StatusOK,
			expectedText:   "This tool approval is no longer available.",
			configureEnvironment: func(e *TestEnvironment, _ *approvalFakeMMClient, _ *model.Post) {
				e.mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
			},
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, _ *model.Post, clearedPost *model.Post) {
				require.Nil(t, clearedPost.GetProp(model.PostPropsAttachments))
				require.Empty(t, mmClient.updatedPosts)
			},
		},
		{
			name:           "invalid context returns ephemeral error",
			requestUserID:  testUserID,
			context:        map[string]any{"tool_ids": callToolIDs},
			post:           newToolApprovalPost(string(callToolCallsJSON)),
			expectedStatus: http.StatusOK,
			expectedText:   "This tool approval is no longer available.",
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, _ *model.Post, _ *model.Post) {
				require.Empty(t, mmClient.updatedPosts)
			},
		},
		{
			name:           "unlicensed returns not available",
			requestUserID:  testUserID,
			context:        map[string]any{"stage": "call", "action": "accept_all", "tool_ids": callToolIDs},
			post:           newToolApprovalPost(string(callToolCallsJSON)),
			expectedStatus: http.StatusOK,
			expectedText:   "This tool approval is no longer available.",
			configureEnvironment: func(e *TestEnvironment, _ *approvalFakeMMClient, _ *model.Post) {
				e.api.licenseChecker = &falseLicenseChecker{}
			},
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, _ *model.Post, _ *model.Post) {
				require.Empty(t, mmClient.updatedPosts)
			},
		},
		{
			name:           "channel tool calling disabled returns not available",
			requestUserID:  testUserID,
			context:        map[string]any{"stage": "call", "action": "accept_all", "tool_ids": callToolIDs},
			post:           newToolApprovalPost(string(callToolCallsJSON)),
			expectedStatus: http.StatusOK,
			expectedText:   "This tool approval is no longer available.",
			configureEnvironment: func(e *TestEnvironment, _ *approvalFakeMMClient, _ *model.Post) {
				e.config.enableChannelMentionToolCalling = false
			},
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, _ *model.Post, _ *model.Post) {
				require.Empty(t, mmClient.updatedPosts)
			},
		},
		{
			name:          "missing AllowToolsInChannelProp returns not available",
			requestUserID: testUserID,
			context:       map[string]any{"stage": "call", "action": "accept_all", "tool_ids": callToolIDs},
			post: func() *model.Post {
				p := newToolApprovalPost(string(callToolCallsJSON))
				delete(p.Props, streaming.AllowToolsInChannelProp)
				return p
			}(),
			expectedStatus: http.StatusOK,
			expectedText:   "This tool approval is no longer available.",
			assertions: func(t *testing.T, mmClient *approvalFakeMMClient, _ *model.Post, _ *model.Post) {
				require.Empty(t, mmClient.updatedPosts)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, mmClient := setupToolApprovalTestEnvironment(t)
			defer e.Cleanup(t)

			channel := &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}
			mmClient.channels = map[string]*model.Channel{testChannelID: channel}

			var clearedPost *model.Post
			if tt.post != nil {
				e.mockAPI.On("GetPost", tt.post.Id).Return(tt.post, nil).Maybe()
				e.mockAPI.On("GetChannel", tt.post.ChannelId).Return(channel, nil).Maybe()
				e.mockAPI.On("UpdatePost", mock.AnythingOfType("*model.Post")).Run(func(args mock.Arguments) {
					clearedPost = args.Get(0).(*model.Post).Clone()
				}).Return(func(post *model.Post) *model.Post {
					return post.Clone()
				}, func(*model.Post) *model.AppError {
					return nil
				}).Maybe()
			}
			e.mockAPI.On("GetUser", tt.requestUserID).Return(&model.User{Id: tt.requestUserID, Locale: "en"}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.mockAPI.On("DeletePost", "approval-postid").Return(nil).Maybe()

			if tt.configureEnvironment != nil {
				tt.configureEnvironment(e, mmClient, tt.post)
			}

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, makeToolApprovalRequest(t, tt.requestUserID, tt.context))

			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)
			payload := parseToolApprovalResponse(t, recorder)
			require.Equal(t, tt.expectedText, payload.EphemeralText)

			if tt.assertions != nil {
				tt.assertions(t, mmClient, tt.post, clearedPost)
			}
		})
	}
}

var _ mmapi.Client = (*approvalFakeMMClient)(nil)
