// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"fmt"
	"testing"

	plugini18n "github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestToolApprovalAttachments(t *testing.T) {
	tests := []struct {
		name            string
		build           func(string, string, []llm.ToolCall, plugini18n.TranslationFunc) []*model.SlackAttachment
		expectedStage   string
		expectedTextKey string
		expectedActions []struct {
			name   string
			action string
			style  string
		}
		assertText func(*testing.T, string)
	}{
		{
			name:            "tool call approval builds attachment with expected actions",
			build:           ToolCallApprovalAttachments,
			expectedStage:   "call",
			expectedTextKey: "agents.tool_approval.call_description",
			expectedActions: []struct {
				name   string
				action string
				style  string
			}{
				{name: "agents.tool_approval.accept_all", action: "accept_all", style: "good"},
				{name: "agents.tool_approval.reject_all", action: "reject_all", style: "danger"},
			},
			assertText: func(t *testing.T, text string) {
				t.Helper()
				require.Contains(t, text, "agents.tool_approval.call_description")
				require.Contains(t, text, "search_docs")
				require.Contains(t, text, "list_channels")
			},
		},
		{
			name:            "tool result approval builds attachment with expected actions",
			build:           ToolResultApprovalAttachments,
			expectedStage:   "result",
			expectedTextKey: "agents.tool_approval.result_description",
			expectedActions: []struct {
				name   string
				action string
				style  string
			}{
				{name: "agents.tool_approval.share_results", action: "share_results", style: "good"},
				{name: "agents.tool_approval.keep_private", action: "keep_private", style: "danger"},
			},
			assertText: func(t *testing.T, text string) {
				t.Helper()
				require.Equal(t, "agents.tool_approval.result_description", text)
			},
		},
	}

	toolCalls := []llm.ToolCall{
		{ID: "tool-1", Name: "search_docs"},
		{ID: "tool-2", Name: "list_channels"},
	}

	translationFunc := func(translationID string, _ string, params ...any) string {
		if len(params) == 0 {
			return translationID
		}
		return fmt.Sprintf("%s:%s", translationID, fmt.Sprint(params...))
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			attachments := testCase.build("plugin-id", "post-id", toolCalls, translationFunc)

			require.Len(t, attachments, 1)
			attachment := attachments[0]
			require.NotNil(t, attachment)
			require.Len(t, attachment.Actions, 2)
			require.NotEmpty(t, attachment.Fallback)
			testCase.assertText(t, attachment.Text)

			for index, expectedAction := range testCase.expectedActions {
				action := attachment.Actions[index]
				require.Equal(t, expectedAction.name, action.Name)
				require.Equal(t, expectedAction.style, action.Style)
				require.NotNil(t, action.Integration)
				require.Equal(t, "/plugins/plugin-id/actions/tool_approval", action.Integration.URL)
				require.Equal(t, testCase.expectedStage, action.Integration.Context["stage"])
				require.Equal(t, expectedAction.action, action.Integration.Context["action"])

				toolIDs, ok := action.Integration.Context["tool_ids"].([]string)
				require.True(t, ok)
				require.Equal(t, []string{"tool-1", "tool-2"}, toolIDs)
			}

			require.NotSame(t, attachment.Actions[0].Integration, attachment.Actions[1].Integration)
		})
	}
}

func TestToolApprovalAttachmentsEmptyToolCalls(t *testing.T) {
	tests := []struct {
		name  string
		build func(string, string, []llm.ToolCall, plugini18n.TranslationFunc) []*model.SlackAttachment
	}{
		{name: "tool call approval returns nil", build: ToolCallApprovalAttachments},
		{name: "tool result approval returns nil", build: ToolResultApprovalAttachments},
	}

	translationFunc := func(translationID string, _ string, params ...any) string {
		if len(params) == 0 {
			return translationID
		}
		return fmt.Sprintf("%s:%s", translationID, fmt.Sprint(params...))
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			require.Nil(t, testCase.build("plugin-id", "", nil, translationFunc))
			require.Nil(t, testCase.build("plugin-id", "", []llm.ToolCall{}, translationFunc))
		})
	}
}

func TestClearApprovalAttachments(t *testing.T) {
	post := &model.Post{}
	post.AddProp(model.PostPropsAttachments, []*model.SlackAttachment{{Text: "attachment"}})
	post.AddProp("other_prop", "keep-me")

	ClearApprovalAttachments(post)

	require.Nil(t, post.GetProp(model.PostPropsAttachments))
	require.Equal(t, "keep-me", post.GetProp("other_prop"))
}
