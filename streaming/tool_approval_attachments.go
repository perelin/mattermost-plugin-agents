// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"fmt"
	"strings"

	plugini18n "github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

const toolApprovalActionURLFormat = "/plugins/%s/actions/tool_approval"

// ToolApprovalPostIDProp is the post prop key on the bot post that stores the ID
// of the separate approval post created for mobile interactive buttons.
const ToolApprovalPostIDProp = "tool_approval_post_id"

func ToolCallApprovalAttachments(pluginID string, originalPostID string, toolCalls []llm.ToolCall, T plugini18n.TranslationFunc) []*model.SlackAttachment {
	if len(toolCalls) == 0 {
		return nil
	}

	toolNames := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		toolNames = append(toolNames, toolCall.Name)
	}

	return []*model.SlackAttachment{buildToolApprovalAttachment(
		pluginID,
		originalPostID,
		T("agents.tool_approval.call_description", "Agents wants to run: %s", strings.Join(toolNames, ", ")),
		"call",
		toolCalls,
		[]toolApprovalActionDefinition{
			{name: T("agents.tool_approval.accept_all", "Accept All"), action: "accept_all", style: "good"},
			{name: T("agents.tool_approval.reject_all", "Reject All"), action: "reject_all", style: "danger"},
		},
	)}
}

func ToolResultApprovalAttachments(pluginID string, originalPostID string, toolCalls []llm.ToolCall, T plugini18n.TranslationFunc) []*model.SlackAttachment {
	if len(toolCalls) == 0 {
		return nil
	}

	return []*model.SlackAttachment{buildToolApprovalAttachment(
		pluginID,
		originalPostID,
		T("agents.tool_approval.result_description", "Tool results are ready for review"),
		"result",
		toolCalls,
		[]toolApprovalActionDefinition{
			{name: T("agents.tool_approval.share_results", "Share Results"), action: "share_results", style: "good"},
			{name: T("agents.tool_approval.keep_private", "Keep Private"), action: "keep_private", style: "danger"},
		},
	)}
}

// ClearApprovalAttachments removes interactive attachment props from the post
// and returns the approval post ID (if any) so the caller can delete it.
func ClearApprovalAttachments(post *model.Post) string {
	post.DelProp(model.PostPropsAttachments)
	approvalPostID, _ := post.GetProp(ToolApprovalPostIDProp).(string)
	post.DelProp(ToolApprovalPostIDProp)
	return approvalPostID
}

type toolApprovalActionDefinition struct {
	name   string
	action string
	style  string
}

func buildToolApprovalAttachment(pluginID string, originalPostID string, text string, stage string, toolCalls []llm.ToolCall, actionDefinitions []toolApprovalActionDefinition) *model.SlackAttachment {
	toolIDs := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		toolIDs = append(toolIDs, toolCall.ID)
	}

	actions := make([]*model.PostAction, 0, len(actionDefinitions))
	for _, actionDefinition := range actionDefinitions {
		actions = append(actions, &model.PostAction{
			Name:  actionDefinition.name,
			Style: actionDefinition.style,
			Integration: &model.PostActionIntegration{
				URL: fmt.Sprintf(toolApprovalActionURLFormat, pluginID),
				Context: map[string]any{
					"stage":            stage,
					"action":           actionDefinition.action,
					"tool_ids":         append([]string(nil), toolIDs...),
					"original_post_id": originalPostID,
				},
			},
		})
	}

	return &model.SlackAttachment{
		Fallback: text,
		Text:     text,
		Actions:  actions,
	}
}
