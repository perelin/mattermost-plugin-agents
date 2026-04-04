// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/require"
)

func TestBuildSkippedImagesNote(t *testing.T) {
	bundle := i18n.Init()
	T := i18n.LocalizerFunc(bundle, "en")

	tests := []struct {
		name            string
		skipped         []llm.SkippedFile
		contains        []string
		wantEmptyResult bool
	}{
		{
			name:            "zero skipped files",
			skipped:         nil,
			wantEmptyResult: true,
		},
		{
			name: "single skipped image",
			skipped: []llm.SkippedFile{
				{Name: "photo.jpg", Size: 8 * 1024 * 1024, Limit: 5 * 1024 * 1024},
			},
			contains: []string{"photo.jpg", "8.0 MB raw", "10.7 MB encoded", "5 MB"},
		},
		{
			name: "multiple skipped images",
			skipped: []llm.SkippedFile{
				{Name: "a.jpg", Size: 6 * 1024 * 1024, Limit: 5 * 1024 * 1024},
				{Name: "b.png", Size: 9 * 1024 * 1024, Limit: 5 * 1024 * 1024},
			},
			contains: []string{"2", "a.jpg", "b.png", "5 MB", "8.0 MB", "12.0 MB"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			note := buildSkippedImagesNote(T, tc.skipped)
			if tc.wantEmptyResult {
				require.Empty(t, note)
				return
			}
			for _, substr := range tc.contains {
				require.Contains(t, note, substr)
			}
		})
	}
}
