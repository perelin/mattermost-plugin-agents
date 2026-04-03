// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestStreamToPostAppendsPostfixMessage(t *testing.T) {
	const (
		postID    = "post-id"
		channelID = "channel-id"
	)

	tests := []struct {
		name            string
		llmResponse     string
		postfixMessage  string
		wantSuffix      string
		wantNoSeparator bool
	}{
		{
			name:           "postfix appended with separator",
			llmResponse:    "Hello, world!",
			postfixMessage: "Note: 1 image was skipped.",
			wantSuffix:     "\n\n---\nNote: 1 image was skipped.",
		},
		{
			name:            "empty postfix adds nothing",
			llmResponse:     "Hello, world!",
			postfixMessage:  "",
			wantNoSeparator: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeStreamingClient{
				channels: map[string]*model.Channel{
					channelID: {Id: channelID, Type: model.ChannelTypeOpen},
				},
			}

			bundle := i18n.Init()
			service := NewMMPostStreamService(client, bundle)

			post := &model.Post{
				Id:        postID,
				ChannelId: channelID,
				Message:   "",
			}

			streamChannel := make(chan llm.TextStreamEvent, 2)
			streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: tc.llmResponse}
			streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			close(streamChannel)

			stream := &llm.TextStreamResult{
				Stream:         streamChannel,
				PostfixMessage: tc.postfixMessage,
			}

			service.StreamToPost(context.Background(), stream, post, "en")

			require.GreaterOrEqual(t, len(client.updatedPosts), 1)
			finalPost := client.updatedPosts[len(client.updatedPosts)-1]

			if tc.wantNoSeparator {
				require.False(t, strings.Contains(finalPost.Message, "---"),
					"expected no separator in message, got: %q", finalPost.Message)
			} else {
				require.True(t, strings.HasSuffix(finalPost.Message, tc.wantSuffix),
					"expected message to end with %q, got: %q", tc.wantSuffix, finalPost.Message)
			}
		})
	}
}
