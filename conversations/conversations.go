// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/format"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/mmtools"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	"github.com/mattermost/mattermost-plugin-ai/threads"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const ThreadIDProp = "referenced_thread"
const AnalysisTypeProp = "prompt_type"

// AIThread represents a user's conversation with an AI
type AIThread struct {
	ID         string `json:"id"`
	Message    string `json:"message"`
	Title      string `json:"title"`
	ChannelID  string `json:"channel_id"`
	ReplyCount int    `json:"reply_count"`
	UpdateAt   int64  `json:"update_at"`
}

// ConfigProvider provides configuration values for conversation behavior
type ConfigProvider interface {
	EnableChannelMentionToolCalling() bool
	AllowNativeWebSearchInChannels() bool
}

type Conversations struct {
	prompts          *llm.Prompts
	mmClient         mmapi.Client
	streamingService streaming.Service
	contextBuilder   *llmcontext.Builder
	bots             *bots.MMBots
	db               *mmapi.DBClient
	licenseChecker   *enterprise.LicenseChecker
	i18n             *i18n.Bundle
	meetingsService  MeetingsService
	configProvider   ConfigProvider
}

// MeetingsService defines the interface for meetings functionality needed by conversations
type MeetingsService interface {
	GetCaptionsFileIDFromProps(post *model.Post) (fileID string, err error)
	SummarizeTranscription(bot *bots.Bot, transcription *subtitles.Subtitles, context *llm.Context) (*llm.TextStreamResult, error)
}

func New(
	prompts *llm.Prompts,
	mmClient mmapi.Client,
	streamingService streaming.Service,
	contextBuilder *llmcontext.Builder,
	botsService *bots.MMBots,
	db *mmapi.DBClient,
	licenseChecker *enterprise.LicenseChecker,
	i18nBundle *i18n.Bundle,
	meetingsService MeetingsService,
	configProvider ConfigProvider,
) *Conversations {
	return &Conversations{
		prompts:          prompts,
		mmClient:         mmClient,
		streamingService: streamingService,
		contextBuilder:   contextBuilder,
		bots:             botsService,
		db:               db,
		licenseChecker:   licenseChecker,
		i18n:             i18nBundle,
		meetingsService:  meetingsService,
		configProvider:   configProvider,
	}
}

// SetMeetingsService sets the meetings service (used to break circular dependency during initialization)
func (c *Conversations) SetMeetingsService(meetingsService MeetingsService) {
	c.meetingsService = meetingsService
}

// ProcessUserRequestWithContext is an internal helper that uses an existing context to process a message
func (c *Conversations) ProcessUserRequestWithContext(bot *bots.Bot, postingUser *model.User, channel *model.Channel, post *model.Post, context *llm.Context, allowToolsInChannel bool) (*llm.TextStreamResult, error) {
	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)
	toolsDisabled := !isDM && !allowToolsInChannel
	if context != nil {
		if toolsDisabled && context.Tools != nil {
			context.DisabledToolsInfo = context.Tools.GetToolsInfo()
		} else {
			context.DisabledToolsInfo = nil
		}
	}

	var posts []llm.Post
	if post.RootId == "" {
		// A new conversation
		prompt, err := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, context)
		if err != nil {
			return nil, fmt.Errorf("failed to format prompt: %w", err)
		}
		posts = []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: prompt,
			},
		}
	} else {
		// Continuing an existing conversation
		previousConversation, errThread := mmapi.GetThreadData(c.mmClient, post.Id)
		if errThread != nil {
			return nil, fmt.Errorf("failed to get previous conversation: %w", errThread)
		}
		previousConversation.CutoffBeforePostID(post.Id)

		var err error
		posts, err = c.existingConversationToLLMPosts(bot, previousConversation, context)
		if err != nil {
			return nil, fmt.Errorf("failed to convert existing conversation to LLM posts: %w", err)
		}
	}

	posts = append(posts, c.PostToAIPost(bot, post))

	completionRequest := llm.CompletionRequest{
		Posts:   posts,
		Context: context,
	}
	var opts []llm.LanguageModelOption
	if toolsDisabled {
		// Tools are disabled in this context but we still inform the LLM about DM-only tools.
		opts = append(opts, llm.WithToolsDisabled())

		if c.configProvider != nil && c.configProvider.AllowNativeWebSearchInChannels() && bot.HasNativeWebSearchEnabled() {
			opts = append(opts, llm.WithNativeWebSearchAllowed())
		}
	}
	result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
	if err != nil {
		return nil, err
	}

	// Decorate the stream with web search annotations if available
	webSearchData := mmtools.ConsumeWebSearchContexts(context)
	c.mmClient.LogDebug("Checking for web search data in ProcessUserRequestWithContext", "has_data", len(webSearchData) > 0, "num_contexts", len(webSearchData))
	if len(webSearchData) > 0 {
		result = mmtools.DecorateStreamWithAnnotations(result, webSearchData, nil)
	}

	go func() {
		request := "Write a short title for the following request. Include only the title and nothing else, no quotations. Request:\n" + post.Message
		if err := c.GenerateTitle(bot, request, post.Id, context); err != nil {
			c.mmClient.LogError("Failed to generate title", "error", err.Error())
			return
		}
	}()

	return result, nil
}

// ProcessUserRequest processes a user request to a bot
func (c *Conversations) ProcessUserRequest(bot *bots.Bot, postingUser *model.User, channel *model.Channel, post *model.Post, allowToolsInChannel bool) (*llm.TextStreamResult, error) {
	// Extract web search context from conversation history to preserve citations
	// This ensures citations from previous searches work in follow-up messages
	webSearchParams := c.extractWebSearchContext(post)

	var contextOpts []llm.ContextOption
	contextOpts = append(contextOpts, c.contextBuilder.WithLLMContextTools(bot))
	if len(webSearchParams) > 0 {
		contextOpts = append(contextOpts, c.contextBuilder.WithLLMContextParameters(webSearchParams))
	}

	// Create a context with default tools and preserved web search context
	llmContext := c.contextBuilder.BuildLLMContextUserRequest(
		bot,
		postingUser,
		channel,
		contextOpts...,
	)

	// If web search context wasn't found, initialize fresh tracking
	if llmContext.Parameters == nil {
		llmContext.Parameters = make(map[string]interface{})
	}
	if _, hasCount := llmContext.Parameters[mmtools.WebSearchCountKey]; !hasCount {
		llmContext.Parameters[mmtools.WebSearchCountKey] = 0
	}
	if _, hasQueries := llmContext.Parameters[mmtools.WebSearchExecutedQueriesKey]; !hasQueries {
		llmContext.Parameters[mmtools.WebSearchExecutedQueriesKey] = []string{}
	}

	// Check for auth errors in the tool store
	if llmContext.Tools != nil {
		authErrors := llmContext.Tools.GetAuthErrors()
		if len(authErrors) > 0 {
			rootID := post.RootId
			if rootID == "" {
				rootID = post.Id
			}
			c.sendOAuthNotifications(bot, postingUser.Id, channel.Id, rootID, authErrors)
		}
	}

	return c.ProcessUserRequestWithContext(bot, postingUser, channel, post, llmContext, allowToolsInChannel)
}

func (c *Conversations) GenerateTitle(bot *bots.Bot, request string, postID string, context *llm.Context) error {
	titleRequest := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: request}},
		Context: context,
	}

	conversationTitle, err := bot.LLM().ChatCompletionNoStream(titleRequest, llm.WithMaxGeneratedTokens(25), llm.WithReasoningDisabled())
	if err != nil {
		return fmt.Errorf("failed to get title: %w", err)
	}

	conversationTitle = strings.Trim(conversationTitle, "\n \"'")

	if err := c.SaveTitle(postID, conversationTitle); err != nil {
		return fmt.Errorf("failed to save title: %w", err)
	}

	return nil
}

// existingConversationToLLMPosts converts existing conversation to LLM posts format
func (c *Conversations) existingConversationToLLMPosts(bot *bots.Bot, conversation *mmapi.ThreadData, context *llm.Context) ([]llm.Post, error) {
	// Handle thread summarization requests
	originalThreadID, ok := conversation.Posts[0].GetProp(ThreadIDProp).(string)
	if ok && originalThreadID != "" && conversation.Posts[0].UserId == bot.GetMMBot().UserId {
		threadPost, err := c.mmClient.GetPost(originalThreadID)
		if err != nil {
			return nil, err
		}
		threadChannel, err := c.mmClient.GetChannel(threadPost.ChannelId)
		if err != nil {
			return nil, err
		}

		if !c.mmClient.HasPermissionToChannel(context.RequestingUser.Id, threadChannel.Id, model.PermissionReadChannel) ||
			c.bots.CheckUsageRestrictions(context.RequestingUser.Id, bot, threadChannel) != nil {
			T := i18n.LocalizerFunc(c.i18n, context.RequestingUser.Locale)
			responsePost := &model.Post{
				ChannelId: context.Channel.Id,
				RootId:    originalThreadID,
				Message:   T("agents.no_longer_access_error", "Sorry, you no longer have access to the original thread."),
			}
			if err = c.BotCreateNonResponsePost(bot.GetMMBot().UserId, context.RequestingUser.Id, responsePost); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("user no longer has access to original thread")
		}

		analysisType, ok := conversation.Posts[0].GetProp(AnalysisTypeProp).(string)
		if !ok {
			return nil, fmt.Errorf("missing analysis type")
		}

		posts, err := threads.New(bot.LLM(), c.prompts, c.mmClient).FollowUpAnalyze(originalThreadID, context, analysisType)
		if err != nil {
			return nil, err
		}
		posts = append(posts, c.ThreadToLLMPosts(bot, conversation)...)
		return posts, nil
	}

	// Plain DM conversation
	prompt, err := c.prompts.Format(prompts.PromptDirectMessageQuestionSystem, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format prompt: %w", err)
	}
	posts := []llm.Post{
		{
			Role:    llm.PostRoleSystem,
			Message: prompt,
		},
	}
	posts = append(posts, c.ThreadToLLMPosts(bot, conversation)...)

	return posts, nil
}

// GetAIThreads gets AI conversation threads for a user
func (c *Conversations) GetAIThreads(userID string) ([]AIThread, error) {
	allBots := c.bots.GetAllBots()

	dmChannelIDs := []string{}
	for _, bot := range allBots {
		channelName := model.GetDMNameFromIds(userID, bot.GetMMBot().UserId)
		botDMChannel, err := c.mmClient.GetChannelByName("", channelName, false)
		if err != nil {
			if errors.Is(err, pluginapi.ErrNotFound) {
				// Channel doesn't exist yet, so we'll skip it
				continue
			}
			c.mmClient.LogError("unable to get DM channel for bot", "error", err, "bot_id", bot.GetMMBot().UserId)
			continue
		}

		// Extra permissions checks are not totally necessary since a user should always have permission to read their own DMs
		if !c.mmClient.HasPermissionToChannel(userID, botDMChannel.Id, model.PermissionReadChannel) {
			c.mmClient.LogDebug("user doesn't have permission to read channel", "user_id", userID, "channel_id", botDMChannel.Id, "bot_id", bot.GetMMBot().UserId)
			continue
		}

		dmChannelIDs = append(dmChannelIDs, botDMChannel.Id)
	}

	return c.getAIThreads(dmChannelIDs)
}

const defaultMaxFileSize = int64(1024 * 1024 * 5) // 5MB

func (c *Conversations) BotCreateNonResponsePost(botid string, requesterUserID string, post *model.Post) error {
	streaming.ModifyPostForBot(botid, requesterUserID, post, "")
	post.AddProp(streaming.NoRegen, true)

	if err := c.mmClient.CreatePost(post); err != nil {
		return err
	}

	return nil
}

func humanReadableSize(bytes int64) string {
	const mb = 1024 * 1024
	if bytes >= mb {
		return fmt.Sprintf("%.0fMB", float64(bytes)/float64(mb))
	}
	const kb = 1024
	return fmt.Sprintf("%.0fKB", float64(bytes)/float64(kb))
}

type skippedFile struct {
	name   string
	reason string
}

func formatSkippedFilesNote(skipped []skippedFile, supportedTypes []string) string {
	if len(skipped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n[Note: The following attached files could not be processed:\n")
	for _, f := range skipped {
		fmt.Fprintf(&b, "- %s (%s)\n", f.name, f.reason)
	}

	// Build human-readable type list from MIME types
	typeNames := make([]string, 0, len(supportedTypes))
	for _, t := range supportedTypes {
		// "image/jpeg" -> "JPEG"
		parts := strings.SplitN(t, "/", 2)
		if len(parts) == 2 {
			typeNames = append(typeNames, strings.ToUpper(parts[1]))
		}
	}
	fmt.Fprintf(&b, "Only text files and images (%s) can be processed.]", strings.Join(typeNames, ", "))
	return b.String()
}

func fileSkipReason(fileInfo *model.FileInfo, enableVision bool, constraints llm.FileConstraints) string {
	isImage := strings.HasPrefix(fileInfo.MimeType, "image/")

	if isImage && !enableVision {
		return "image processing is not enabled"
	}
	if isImage && !constraints.HasSupportedImageType(fileInfo.MimeType) {
		supported := make([]string, 0, len(constraints.SupportedImageTypes))
		for _, t := range constraints.SupportedImageTypes {
			parts := strings.SplitN(t, "/", 2)
			if len(parts) == 2 {
				supported = append(supported, strings.ToUpper(parts[1]))
			}
		}
		return fmt.Sprintf("unsupported image format, supported: %s", strings.Join(supported, ", "))
	}
	if isImage && constraints.MaxImageSize > 0 && fileInfo.Size > constraints.MaxImageSize {
		return fmt.Sprintf("image too large: %s, maximum: %s",
			humanReadableSize(fileInfo.Size), humanReadableSize(constraints.MaxImageSize))
	}

	return "file type not supported"
}

func (c *Conversations) PostToAIPost(bot *bots.Bot, post *model.Post) llm.Post {
	var filesForUpstream []llm.File
	message := format.PostBody(post)
	var extractedFileContents []string

	maxFileSize := defaultMaxFileSize
	if bot.GetConfig().MaxFileSize > 0 {
		maxFileSize = bot.GetConfig().MaxFileSize
	}

	constraints := bot.LLM().FileConstraints()
	var skipped []skippedFile

	for _, fileID := range post.FileIds {
		fileInfo, err := c.mmClient.GetFileInfo(fileID)
		if err != nil {
			c.mmClient.LogError("Error getting file info", "error", err)
			continue
		}

		// Check for files that have been interpreted already by the server or are text files.
		content := ""
		if trimmedContent := strings.TrimSpace(fileInfo.Content); trimmedContent != "" {
			content = trimmedContent
		} else if strings.HasPrefix(fileInfo.MimeType, "text/") {
			file, err := c.mmClient.GetFile(fileID)
			if err != nil {
				c.mmClient.LogError("Error getting file", "error", err)
				continue
			}
			contentBytes, err := io.ReadAll(io.LimitReader(file, maxFileSize))
			if err != nil {
				c.mmClient.LogError("Error reading file content", "error", err)
				continue
			}
			content = string(contentBytes)
			if int64(len(contentBytes)) == maxFileSize {
				content += "\n... (content truncated due to size limit)"
			}
		} else if bot.GetConfig().EnableVision && strings.HasPrefix(fileInfo.MimeType, "image/") &&
			constraints.HasSupportedImageType(fileInfo.MimeType) &&
			(constraints.MaxImageSize == 0 || fileInfo.Size <= constraints.MaxImageSize) {
			// Valid image — fetch and add to upstream files
			file, err := c.mmClient.GetFile(fileID)
			if err != nil {
				c.mmClient.LogError("Error getting file", "error", err)
				continue
			}
			filesForUpstream = append(filesForUpstream, llm.File{
				Reader:   file,
				MimeType: fileInfo.MimeType,
				Size:     fileInfo.Size,
			})
		} else if strings.TrimSpace(fileInfo.Content) == "" && !strings.HasPrefix(fileInfo.MimeType, "text/") {
			// File is not text, not server-extracted, and not a valid image — skip with reason
			skipped = append(skipped, skippedFile{
				name:   fileInfo.Name,
				reason: fileSkipReason(fileInfo, bot.GetConfig().EnableVision, constraints),
			})
		}

		if content != "" {
			fileContent := fmt.Sprintf("File Name: %s\nContent: %s", fileInfo.Name, content)
			extractedFileContents = append(extractedFileContents, fileContent)
		}
	}

	// Add structured file contents to the message
	if len(extractedFileContents) > 0 {
		message += "\nAttached File Contents:\n" + strings.Join(extractedFileContents, "\n\n")
	}

	// Add note about skipped files
	if note := formatSkippedFilesNote(skipped, constraints.SupportedImageTypes); note != "" {
		message += note
	}

	role := llm.PostRoleUser
	if c.bots.IsAnyBot(post.UserId) {
		role = llm.PostRoleBot
	}

	// Check for tools
	pendingToolsProp := post.GetProp(streaming.ToolCallProp)
	tools := []llm.ToolCall{}
	pendingTools, ok := pendingToolsProp.(string)
	if ok {
		var toolCalls []llm.ToolCall
		if err := json.Unmarshal([]byte(pendingTools), &toolCalls); err != nil {
			c.mmClient.LogError("Error unmarshalling tool calls", "error", err)
		} else {
			for _, toolCall := range toolCalls {
				if toolCall.Status == llm.ToolCallStatusRejected {
					continue
				}
				tools = append(tools, toolCall)
			}
		}
	}

	// Check for reasoning/thinking content
	reasoning := ""
	if reasoningProp := post.GetProp(streaming.ReasoningSummaryProp); reasoningProp != nil {
		if reasoningStr, ok := reasoningProp.(string); ok {
			reasoning = reasoningStr
		}
	}

	// Check for reasoning signature (opaque verification field)
	reasoningSignature := ""
	if signatureProp := post.GetProp(streaming.ReasoningSignatureProp); signatureProp != nil {
		if signatureStr, ok := signatureProp.(string); ok {
			reasoningSignature = signatureStr
		}
	}

	return llm.Post{
		Role:               role,
		Message:            message,
		Files:              filesForUpstream,
		ToolUse:            tools,
		Reasoning:          reasoning,
		ReasoningSignature: reasoningSignature,
	}
}

func (c *Conversations) ThreadToLLMPosts(bot *bots.Bot, threadData *mmapi.ThreadData) []llm.Post {
	result := make([]llm.Post, 0, len(threadData.Posts))

	for _, post := range threadData.Posts {
		aiPost := c.PostToAIPost(bot, post)

		// Add username prefix for user messages in multi-user threads
		if aiPost.Role == llm.PostRoleUser {
			if user, exists := threadData.UsersByID[post.UserId]; exists {
				aiPost.Message = "@" + user.Username + ": " + aiPost.Message
			}
		}

		result = append(result, aiPost)
	}

	return result
}

// sendOAuthNotifications sends an ephemeral post to notify the user about MCP servers that require authentication
func (c *Conversations) sendOAuthNotifications(bot *bots.Bot, userID, channelID, rootID string, authErrors []llm.ToolAuthError) {
	if len(authErrors) == 0 {
		return
	}

	// Build the message
	var message strings.Builder
	message.WriteString("**Authentication Required**\n\n")
	message.WriteString("The following MCP servers require authentication:\n\n")

	for _, authErr := range authErrors {
		message.WriteString(fmt.Sprintf("• **%s**: [Click here to authenticate](%s)\n", authErr.ServerName, authErr.AuthURL))
	}

	message.WriteString("\nPlease authenticate with the required servers and try again.")

	// Create the ephemeral post
	post := &model.Post{
		RootId:    rootID,
		UserId:    bot.GetMMBot().UserId,
		ChannelId: channelID,
		Message:   message.String(),
	}

	// Send the ephemeral post
	c.mmClient.SendEphemeralPost(userID, post)
}
