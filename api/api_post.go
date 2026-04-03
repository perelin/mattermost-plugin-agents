// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	stdcontext "context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	plugini18n "github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/react"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost-plugin-ai/threads"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	TitleThreadSummary     = "Thread Summary"
	TitleFindActionItems   = "Action Items"
	TitleFindOpenQuestions = "Open Questions"
)

func (a *API) postAuthorizationRequired(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	postID := c.Param("postid")

	post, err := a.pluginAPI.Post.GetPost(postID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.Set(ContextPostKey, post)

	channel, err := a.pluginAPI.Channel.Get(post.ChannelId)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.Set(ContextChannelKey, channel)

	if !a.pluginAPI.User.HasPermissionToChannel(userID, channel.Id, model.PermissionReadChannel) {
		c.AbortWithError(http.StatusForbidden, errors.New("user doesn't have permission to read channel post in in"))
		return
	}

	bot := c.MustGet(ContextBotKey).(*bots.Bot)
	if err := a.bots.CheckUsageRestrictions(userID, bot, channel); err != nil {
		c.AbortWithError(http.StatusForbidden, err)
		return
	}
}

func (a *API) handleReact(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	requestingUser, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	context := a.contextBuilder.BuildLLMContextUserRequest(
		bot,
		requestingUser,
		channel,
	)

	emojiName, err := react.New(
		bot.LLM(),
		a.prompts,
	).Resolve(post.Message, context)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Add reaction to the post as the requesting user
	if err := a.pluginAPI.Post.AddReaction(&model.Reaction{
		EmojiName: emojiName,
		UserId:    userID,
		PostId:    post.Id,
	}); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to add reaction: %w", err))
	}

	c.Status(http.StatusOK)
}

func (a *API) handleThreadAnalysis(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	var data struct {
		AnalysisType string `json:"analysis_type" binding:"required"`
	}
	if bindErr := c.ShouldBindJSON(&data); bindErr != nil {
		c.AbortWithError(http.StatusBadRequest, bindErr)
		return
	}

	switch data.AnalysisType {
	case "summarize_thread":
		// Valid analysis type for thread summarization
	case "action_items":
		// Valid analysis type for finding action items
	case "open_questions":
		// Valid analysis type for finding open questions
	default:
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid analysis type: %s", data.AnalysisType))
		return
	}

	// Get the user to build context
	user, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to get user: %w", err))
		return
	}

	// Thread analysis disables tools, so skip MCP/tool initialization entirely.
	llmContext := a.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		a.contextBuilder.WithLLMContextNoTools(),
	)

	// Create thread analyzer
	analyzer := threads.New(bot.LLM(), a.prompts, a.mmClient)
	var analysisStream *llm.TextStreamResult
	var title string
	switch data.AnalysisType {
	case "summarize_thread":
		title = TitleThreadSummary
		analysisStream, err = analyzer.Summarize(post.Id, llmContext)
	case "action_items":
		title = TitleFindActionItems
		analysisStream, err = analyzer.FindActionItems(post.Id, llmContext)
	case "open_questions":
		title = TitleFindOpenQuestions
		analysisStream, err = analyzer.FindOpenQuestions(post.Id, llmContext)
	}
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to analyze thread: %w", err))
		return
	}

	// Create analysis post
	siteURL := a.pluginAPI.Configuration.GetConfig().ServiceSettings.SiteURL
	analysisPost := a.makeAnalysisPost(user.Locale, post.Id, data.AnalysisType, *siteURL)
	if err := a.streamingService.StreamToNewDM(stdcontext.Background(), bot.GetMMBot().UserId, analysisStream, user.Id, analysisPost, post.Id); err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	a.conversationsService.SaveTitleAsync(post.Id, title)

	c.JSON(http.StatusOK, map[string]string{
		"postid":    analysisPost.Id,
		"channelid": analysisPost.ChannelId,
	})
}

func (a *API) handleTranscribeFile(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	fileID := c.Param("fileid")
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	result, err := a.meetingsService.HandleTranscribeFile(userID, bot, post, channel, fileID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.Render(http.StatusOK, render.JSON{Data: result})
}

func (a *API) handleSummarizeTranscription(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)
	bot := c.MustGet(ContextBotKey).(*bots.Bot)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	result, err := a.meetingsService.HandleSummarizeTranscription(userID, bot, post, channel)
	if err != nil {
		if err.Error() == "not a calls or zoom bot post" {
			c.AbortWithError(http.StatusBadRequest, errors.New("not a calls or zoom bot post"))
			return
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to summarize transcription: %w", err))
		return
	}

	c.Render(http.StatusOK, render.JSON{Data: result})
}

func (a *API) handleStop(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	botID := post.UserId
	if !a.bots.IsAnyBot(botID) {
		c.AbortWithError(http.StatusBadRequest, errors.New("not a bot post"))
		return
	}

	if post.GetProp(streaming.LLMRequesterUserID) != userID {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original poster can stop the stream"))
		return
	}

	a.streamingService.StopStreaming(post.Id)
	c.Status(http.StatusOK)
}

func (a *API) handleRegenerate(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	err := a.conversationsService.HandleRegenerate(userID, post, channel)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to regenerate post: %w", err))
		return
	}

	c.Status(http.StatusOK)
}

func (a *API) handleToolCall(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	// Defense-in-depth: block channel tool calls if config flag is off.
	// Use post.UserId (the bot that created the post) to check the DM,
	// because the botUsername query parameter may resolve to a different bot.
	isDM := mmapi.IsDMWith(post.UserId, channel)
	if !isDM && !a.config.EnableChannelMentionToolCalling() {
		c.AbortWithError(http.StatusForbidden, errors.New("channel tool calling is disabled"))
		return
	}

	// Only the original requester can approve/reject tool calls
	if post.GetProp(streaming.LLMRequesterUserID) != userID {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original requester can approve/reject tool calls"))
		return
	}

	var data struct {
		AcceptedToolIDs []string `json:"accepted_tool_ids" binding:"required"`
	}

	if err := c.ShouldBindJSON(&data); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	err := a.conversationsService.HandleToolCall(userID, post, channel, data.AcceptedToolIDs)
	if err != nil {
		switch {
		case err.Error() == "post missing pending tool calls" || err.Error() == "post pending tool calls not valid JSON":
			c.AbortWithError(http.StatusBadRequest, err)
		case errors.Is(err, conversations.ErrChannelToolCallingDisabled):
			c.AbortWithError(http.StatusForbidden, err)
		default:
			c.AbortWithError(http.StatusInternalServerError, err)
		}
		return
	}

	c.Status(http.StatusOK)
}

func (a *API) handleToolCallPrivate(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	// Defense-in-depth: block channel tool call access if config flag is off.
	// Use post.UserId (the bot that created the post) to check the DM,
	// because the botUsername query parameter may resolve to a different bot.
	isDM := mmapi.IsDMWith(post.UserId, channel)
	if !isDM && !a.config.EnableChannelMentionToolCalling() {
		c.AbortWithError(http.StatusForbidden, errors.New("channel tool calling is disabled"))
		return
	}

	// Only the original requester can view private tool calls
	if post.GetProp(streaming.LLMRequesterUserID) != userID {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original requester can view tool calls"))
		return
	}

	kvKey := streaming.ToolCallPrivateKVKey(post.Id, userID)
	var toolCalls []llm.ToolCall
	if err := a.mmClient.KVGet(kvKey, &toolCalls); err != nil {
		if mmapi.IsKVNotFound(err) {
			c.AbortWithError(http.StatusBadRequest, errors.New("post missing pending tool calls"))
		} else {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to load tool calls from KV store: %w", err))
		}
		return
	}

	c.JSON(http.StatusOK, toolCalls)
}

func (a *API) handleToolResultPrivate(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	// Defense-in-depth: block channel tool result access if config flag is off.
	// Use post.UserId (the bot that created the post) to check the DM,
	// because the botUsername query parameter may resolve to a different bot.
	isDM := mmapi.IsDMWith(post.UserId, channel)
	if !isDM && !a.config.EnableChannelMentionToolCalling() {
		c.AbortWithError(http.StatusForbidden, errors.New("channel tool calling is disabled"))
		return
	}

	// Only the original requester can view private tool results
	if post.GetProp(streaming.LLMRequesterUserID) != userID {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original requester can view tool results"))
		return
	}

	kvKey := streaming.ToolResultPrivateKVKey(post.Id, userID)
	var toolResults []llm.ToolCall
	if err := a.mmClient.KVGet(kvKey, &toolResults); err != nil {
		if mmapi.IsKVNotFound(err) {
			c.AbortWithError(http.StatusBadRequest, errors.New("post missing pending tool results"))
		} else {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to load tool results from KV store: %w", err))
		}
		return
	}

	c.JSON(http.StatusOK, toolResults)
}

func (a *API) handleToolResult(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)
	channel := c.MustGet(ContextChannelKey).(*model.Channel)

	if !a.licenseChecker.IsBasicsLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("feature not licensed"))
		return
	}

	// Defense-in-depth: block channel tool results if config flag is off.
	// Use post.UserId (the bot that created the post) to check the DM,
	// because the botUsername query parameter may resolve to a different bot.
	isDM := mmapi.IsDMWith(post.UserId, channel)
	if !isDM && !a.config.EnableChannelMentionToolCalling() {
		c.AbortWithError(http.StatusForbidden, errors.New("channel tool calling is disabled"))
		return
	}

	// Only the original requester can approve/reject tool results
	if post.GetProp(streaming.LLMRequesterUserID) != userID {
		c.AbortWithError(http.StatusForbidden, errors.New("only the original requester can approve/reject tool results"))
		return
	}

	var data struct {
		AcceptedToolIDs []string `json:"accepted_tool_ids" binding:"required"`
	}

	if err := c.ShouldBindJSON(&data); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := a.conversationsService.HandleToolResult(userID, post, channel, data.AcceptedToolIDs); err != nil {
		switch {
		case err.Error() == "post missing pending tool results" || err.Error() == "post pending tool results not valid JSON":
			c.AbortWithError(http.StatusBadRequest, err)
		case errors.Is(err, conversations.ErrChannelToolCallingDisabled):
			c.AbortWithError(http.StatusForbidden, err)
		default:
			c.AbortWithError(http.StatusInternalServerError, err)
		}
		return
	}

	c.Status(http.StatusOK)
}

func (a *API) handleToolApprovalAction(c *gin.Context) {
	var req model.PostActionIntegrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	userLocale := "en"
	if user, err := a.pluginAPI.User.Get(req.UserId); err == nil && user != nil && user.Locale != "" {
		userLocale = user.Locale
	}
	T := plugini18n.LocalizerFunc(a.i18nBundle, userLocale)
	respond := func(text string) {
		c.JSON(http.StatusOK, &model.PostActionIntegrationResponse{EphemeralText: text})
	}

	post, err := a.pluginAPI.Post.GetPost(req.PostId)
	if err != nil || post == nil {
		respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		return
	}

	if post.GetProp(streaming.LLMRequesterUserID) != req.UserId {
		respond(T("agents.tool_approval.not_authorized", "Only the person who triggered this can approve."))
		return
	}

	channel, err := a.pluginAPI.Channel.Get(post.ChannelId)
	if err != nil || channel == nil {
		respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		return
	}

	if !a.licenseChecker.IsBasicsLicensed() {
		respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		return
	}

	isDM := mmapi.IsDMWith(post.UserId, channel)
	if !isDM && !a.config.EnableChannelMentionToolCalling() {
		respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		return
	}
	if !isDM && post.GetProp(streaming.AllowToolsInChannelProp) != "true" {
		respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		return
	}

	stage, action, toolIDs, ok := parseToolApprovalContext(req.Context)
	if !ok {
		respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		return
	}

	streaming.ClearApprovalAttachments(post)
	if err := a.pluginAPI.Post.UpdatePost(post); err != nil {
		a.pluginAPI.Log.Error("Failed to update post after clearing approval attachments", "error", err)
		respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		return
	}

	var actionErr error
	var successText string
	switch {
	case stage == "call" && action == "accept_all":
		actionErr = a.conversationsService.HandleToolCall(req.UserId, post, channel, toolIDs)
		successText = T("agents.tool_approval.approved", "Tools approved. Processing...")
	case stage == "call" && action == "reject_all":
		actionErr = a.conversationsService.HandleToolCall(req.UserId, post, channel, []string{})
		successText = T("agents.tool_approval.rejected", "Tools rejected.")
	case stage == "result" && action == "share_results":
		actionErr = a.conversationsService.HandleToolResult(req.UserId, post, channel, toolIDs)
		successText = T("agents.tool_approval.shared", "Results shared.")
	case stage == "result" && action == "keep_private":
		actionErr = a.conversationsService.HandleToolResult(req.UserId, post, channel, []string{})
		successText = T("agents.tool_approval.kept_private", "Results kept private.")
	default:
		respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		return
	}

	if actionErr != nil {
		switch actionErr.Error() {
		case "post missing pending tool calls", "post pending tool calls not valid JSON", "post missing pending tool results", "post pending tool results not valid JSON":
			respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		case "only the original requester can approve/reject tool calls", "only the original requester can approve/reject tool results":
			respond(T("agents.tool_approval.not_authorized", "Only the person who triggered this can approve."))
		default:
			a.pluginAPI.Log.Error("Unexpected tool approval action error", "error", actionErr)
			respond(T("agents.tool_approval.not_available", "This tool approval is no longer available."))
		}
		return
	}

	respond(successText)
}

func parseToolApprovalContext(context map[string]any) (string, string, []string, bool) {
	stage, ok := context["stage"].(string)
	if !ok || stage == "" {
		return "", "", nil, false
	}

	action, ok := context["action"].(string)
	if !ok || action == "" {
		return "", "", nil, false
	}

	rawToolIDs, ok := context["tool_ids"]
	if !ok {
		return "", "", nil, false
	}

	switch values := rawToolIDs.(type) {
	case []string:
		return stage, action, append([]string(nil), values...), true
	case []any:
		toolIDs := make([]string, 0, len(values))
		for _, value := range values {
			toolID, ok := value.(string)
			if !ok || toolID == "" {
				return "", "", nil, false
			}
			toolIDs = append(toolIDs, toolID)
		}
		return stage, action, toolIDs, true
	default:
		return "", "", nil, false
	}
}

func (a *API) handlePostbackSummary(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	post := c.MustGet(ContextPostKey).(*model.Post)

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	result, err := a.meetingsService.HandlePostbackSummary(userID, post)
	if err != nil {
		if err.Error() == "post missing reference to transcription post ID" {
			c.AbortWithError(http.StatusBadRequest, err)
		} else {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("unable to post back summary: %w", err))
		}
		return
	}

	c.Render(http.StatusOK, render.JSON{Data: result})
}

// makeAnalysisPost creates a post for thread analysis results
func (a *API) makeAnalysisPost(locale string, postIDToAnalyze string, analysisType string, siteURL string) *model.Post {
	post := &model.Post{}
	post.AddProp(conversations.ThreadIDProp, postIDToAnalyze)
	post.AddProp(conversations.AnalysisTypeProp, analysisType)

	return post
}
