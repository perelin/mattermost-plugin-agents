// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

func makePostToAIPostConversations(t *testing.T, mmClient *fakeMMClient) (*conversations.Conversations, *bots.MMBots) {
	t.Helper()

	promptSet, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()

	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)
	botService := bots.New("p2lab-agents", mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
	c := conversations.New(promptSet, mmClient, nil, nil, botService, nil, licenseChecker, i18n.Init(), nil, nil)

	return c, botService
}

func TestPostToAIPostSkipsOversizedImage(t *testing.T) {
	const (
		fileID = "file-1"
		userID = "user-1"
		botID  = "bot-1"
	)
	const fiveMB = int64(5 * 1024 * 1024)

	tests := []struct {
		name         string
		fileSize     int64
		enableVision bool
		wantFiles    int
		wantSkipped  int
	}{
		{
			name:         "image within limit is sent to LLM",
			fileSize:     1 * 1024 * 1024,
			enableVision: true,
			wantFiles:    1,
			wantSkipped:  0,
		},
		{
			name:         "oversized image is skipped",
			fileSize:     8 * 1024 * 1024,
			enableVision: true,
			wantFiles:    0,
			wantSkipped:  1,
		},
		{
			name:         "image under raw limit but over base64 limit is skipped",
			fileSize:     4 * 1024 * 1024,
			enableVision: true,
			wantFiles:    0,
			wantSkipped:  1,
		},
		{
			name:         "oversized image with vision disabled is not skipped or sent",
			fileSize:     8 * 1024 * 1024,
			enableVision: false,
			wantFiles:    0,
			wantSkipped:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mmClient := &fakeMMClient{
				fileInfos: map[string]*model.FileInfo{
					fileID: {
						Id:       fileID,
						Name:     "photo.jpg",
						MimeType: "image/jpeg",
						Size:     tc.fileSize,
					},
				},
				fileContents: map[string]io.ReadCloser{
					fileID: io.NopCloser(bytes.NewReader([]byte("fake-image-data"))),
				},
			}

			c, botService := makePostToAIPostConversations(t, mmClient)

			bot := bots.NewBot(
				llm.BotConfig{
					ID:           botID,
					Name:         "test-bot",
					EnableVision: tc.enableVision,
					MaxFileSize:  fiveMB,
				},
				llm.ServiceConfig{},
				&model.Bot{UserId: botID, Username: "test-bot"},
				nil,
			)
			botService.SetBotsForTesting([]*bots.Bot{bot})

			post := &model.Post{
				UserId:  userID,
				FileIds: model.StringArray{fileID},
			}

			result := c.PostToAIPost(bot, post)

			require.Len(t, result.Files, tc.wantFiles, "Files count mismatch")
			require.Len(t, result.SkippedFiles, tc.wantSkipped, "SkippedFiles count mismatch")

			if tc.wantSkipped > 0 {
				require.Equal(t, "photo.jpg", result.SkippedFiles[0].Name)
				require.Equal(t, tc.fileSize, result.SkippedFiles[0].Size)
				require.Equal(t, fiveMB, result.SkippedFiles[0].Limit)
			}
		})
	}
}

func TestProcessUserRequestWithContextSetsPostfixMessageForSkippedImages(t *testing.T) {
	const (
		fileID = "file-1"
		userID = "user-1"
		botID  = "bot-1"
	)
	const fiveMB = int64(5 * 1024 * 1024)

	mmClient := &fakeMMClient{
		fileInfos: map[string]*model.FileInfo{
			fileID: {
				Id:       fileID,
				Name:     "photo.jpg",
				MimeType: "image/jpeg",
				Size:     8 * 1024 * 1024,
			},
		},
	}

	c, botService := makePostToAIPostConversations(t, mmClient)

	llmClient := &capturingLanguageModel{}
	bot := bots.NewBot(
		llm.BotConfig{
			ID:           botID,
			Name:         "test-bot",
			EnableVision: true,
			MaxFileSize:  fiveMB,
		},
		llm.ServiceConfig{},
		&model.Bot{UserId: botID, Username: "test-bot"},
		llmClient,
	)
	botService.SetBotsForTesting([]*bots.Bot{bot})

	postingUser := &model.User{Id: userID, Locale: "en"}
	channel := &model.Channel{Type: model.ChannelTypeDirect, Name: userID + "__" + botID}
	post := &model.Post{Id: "post-1", UserId: userID, Message: "describe this", FileIds: model.StringArray{fileID}}
	context := &llm.Context{
		RequestingUser: postingUser,
		Channel:        channel,
		BotName:        "test-bot",
		BotUsername:    "test-bot",
		BotModel:       "test-model",
	}

	result, err := c.ProcessUserRequestWithContext(bot, postingUser, channel, post, context, false)
	require.NoError(t, err)
	require.Equal(t, "Note: The image \"photo.jpg\" (8.0 MB raw, 10.7 MB encoded) was not sent to the AI — it exceeds the 5 MB size limit.", result.PostfixMessage)
}
