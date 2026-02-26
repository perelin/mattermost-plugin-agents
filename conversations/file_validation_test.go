// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	llmmocks "github.com/mattermost/mattermost-plugin-ai/llm/mocks"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestPostToAIPost_FileValidation(t *testing.T) {
	constraints := llm.FileConstraints{
		SupportedImageTypes: []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
		MaxImageSize:        5 * 1024 * 1024 * 3 / 4, // ~3.75MB raw (accounts for base64 inflation to 5MB)
	}

	tests := []struct {
		name              string
		fileInfos         []*model.FileInfo
		enableVision      bool
		expectFiles       int    // number of llm.File in result
		expectContains    string // substring expected in result.Message
		expectNotContains string // substring NOT expected in result.Message
	}{
		{
			name: "supported image within size limit passes through",
			fileInfos: []*model.FileInfo{
				{Id: "f1", Name: "photo.jpg", MimeType: "image/jpeg", Size: 1024},
			},
			enableVision:      true,
			expectFiles:       1,
			expectNotContains: "[Note:",
		},
		{
			name: "unsupported image format produces note",
			fileInfos: []*model.FileInfo{
				{Id: "f1", Name: "photo.tiff", MimeType: "image/tiff", Size: 1024},
			},
			enableVision:   true,
			expectFiles:    0,
			expectContains: "unsupported image format",
		},
		{
			name: "oversized image produces note",
			fileInfos: []*model.FileInfo{
				{Id: "f1", Name: "huge.png", MimeType: "image/png", Size: 10 * 1024 * 1024},
			},
			enableVision:   true,
			expectFiles:    0,
			expectContains: "image too large",
		},
		{
			name: "video file produces note",
			fileInfos: []*model.FileInfo{
				{Id: "f1", Name: "meeting.mp4", MimeType: "video/mp4", Size: 1024},
			},
			enableVision:   true,
			expectFiles:    0,
			expectContains: "file type not supported",
		},
		{
			name: "image with vision disabled produces note",
			fileInfos: []*model.FileInfo{
				{Id: "f1", Name: "photo.jpg", MimeType: "image/jpeg", Size: 1024},
			},
			enableVision:   false,
			expectFiles:    0,
			expectContains: "image processing is not enabled",
		},
		{
			name: "text file still works",
			fileInfos: []*model.FileInfo{
				{Id: "f1", Name: "readme.txt", MimeType: "text/plain", Size: 100},
			},
			enableVision:      true,
			expectFiles:       0, // text files go into message, not Files
			expectContains:    "readme.txt",
			expectNotContains: "[Note:",
		},
		{
			name: "mix of supported and unsupported produces note for unsupported only",
			fileInfos: []*model.FileInfo{
				{Id: "f1", Name: "photo.jpg", MimeType: "image/jpeg", Size: 1024},
				{Id: "f2", Name: "video.mp4", MimeType: "video/mp4", Size: 1024},
			},
			enableVision:   true,
			expectFiles:    1,
			expectContains: "video.mp4",
		},
		{
			name: "file with server-extracted content processed as text",
			fileInfos: []*model.FileInfo{
				{Id: "f1", Name: "document.pdf", MimeType: "application/pdf", Size: 1024, Content: "Extracted PDF text content"},
			},
			enableVision:      true,
			expectFiles:       0,
			expectContains:    "Extracted PDF text content",
			expectNotContains: "[Note:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mmClient := mocks.NewMockClient(t)
			mockLLM := llmmocks.NewMockLanguageModel(t)
			mockLLM.On("FileConstraints").Return(constraints).Maybe()

			botConfig := llm.BotConfig{
				ID:           "botid",
				Name:         "testbot",
				DisplayName:  "Test Bot",
				EnableVision: tc.enableVision,
				ServiceID:    "test-service",
			}
			serviceConfig := llm.ServiceConfig{ID: "test-service", Type: llm.ServiceTypeOpenAI}
			mmBot := &model.Bot{UserId: "botid"}
			bot := bots.NewBot(botConfig, serviceConfig, mmBot, mockLLM)

			post := &model.Post{
				Id:      "post1",
				Message: "test message",
				FileIds: make([]string, len(tc.fileInfos)),
			}
			for i, fi := range tc.fileInfos {
				post.FileIds[i] = fi.Id
				mmClient.On("GetFileInfo", fi.Id).Return(fi, nil)
				fileContent := []byte("file content for " + fi.Name)
				mmClient.On("GetFile", fi.Id).Return(io.NopCloser(bytes.NewReader(fileContent)), nil).Maybe()
			}

			botService := bots.New(nil, nil, nil, nil, nil, nil, nil)
			conv := conversations.New(nil, mmClient, nil, nil, botService, nil, nil, i18n.Init(), nil, nil)

			result := conv.PostToAIPost(bot, post)

			assert.Equal(t, tc.expectFiles, len(result.Files), "unexpected number of files")
			if tc.expectContains != "" {
				assert.Contains(t, result.Message, tc.expectContains)
			}
			if tc.expectNotContains != "" {
				assert.NotContains(t, result.Message, tc.expectNotContains)
			}
		})
	}
}
