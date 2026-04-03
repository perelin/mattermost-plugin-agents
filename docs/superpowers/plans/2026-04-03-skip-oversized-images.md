# Skip Oversized Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Skip images exceeding `MaxFileSize` in `PostToAIPost` and append a static note to the bot's reply post so the user knows the image was not sent to the AI.

**Architecture:** The check lives in `conversations/conversations.go:PostToAIPost` (provider-agnostic). Skipped-file metadata flows from `PostToAIPost` → `ProcessUserRequestWithContext` via `llm.Post.SkippedFiles`, then is attached to `llm.TextStreamResult.PostfixMessage`. `StreamToPost` appends the note to the bot reply at `EventTypeEnd`.

**Tech Stack:** Go stdlib only; `go-i18n` for translations; existing `fakeMMClient` / `fakeStreamingClient` patterns for tests.

---

## File Map

| File | Action | What changes |
|---|---|---|
| `llm/completion_request.go` | Modify | Add `SkippedFile` type; add `SkippedFiles []SkippedFile` to `Post` |
| `llm/stream.go` | Modify | Add `PostfixMessage string` to `TextStreamResult` |
| `i18n/en.json` | Modify | Add 2 translation keys |
| `i18n/es.json` | Modify | Add 2 translation keys |
| `conversations/conversations.go` | Modify | Add `buildSkippedImagesNote` helper; update `PostToAIPost`; update `ProcessUserRequestWithContext` |
| `conversations/tool_handling_test.go` | Modify | Extend `fakeMMClient` with `fileInfos`/`fileContents` maps |
| `conversations/post_to_ai_post_test.go` | Create | Tests for `buildSkippedImagesNote` and `PostToAIPost` |
| `streaming/streaming.go` | Modify | Append `PostfixMessage` at `EventTypeEnd` |
| `streaming/postfix_message_test.go` | Create | Test for postfix appending in `StreamToPost` |

---

## Task 1: Add `SkippedFile` type and `PostfixMessage` field

**Files:**
- Modify: `llm/completion_request.go`
- Modify: `llm/stream.go`

- [ ] **Step 1: Add `SkippedFile` to `llm/completion_request.go`**

In `llm/completion_request.go`, after the `File` struct (after line 16), add:

```go
// SkippedFile records an image that was excluded from the LLM request due to size.
type SkippedFile struct {
	Name  string
	Size  int64 // actual file size in bytes
	Limit int64 // the MaxFileSize limit that was applied
}
```

Then add `SkippedFiles []SkippedFile` to the `Post` struct, after the `Files` field:

```go
type Post struct {
	Role               PostRole
	Message            string
	Files              []File
	SkippedFiles       []SkippedFile
	ToolUse            []ToolCall
	Reasoning          string
	ReasoningSignature string
}
```

- [ ] **Step 2: Add `PostfixMessage` to `llm/stream.go`**

In `llm/stream.go`, change `TextStreamResult` to:

```go
// TextStreamResult represents a stream of text events
type TextStreamResult struct {
	Stream         <-chan TextStreamEvent
	PostfixMessage string // static note appended to the bot reply post; set by caller before passing to StreamToPost
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./llm/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add llm/completion_request.go llm/stream.go
git commit -m "feat(llm): add SkippedFile type and PostfixMessage to TextStreamResult"
```

---

## Task 2: Add i18n strings

**Files:**
- Modify: `i18n/en.json`
- Modify: `i18n/es.json`

- [ ] **Step 1: Add entries to `i18n/en.json`**

Append before the closing `]`:

```json
  ,{
    "id": "agents.skipped_image_single",
    "translation": "Note: The image \"%s\" (%s) was not sent to the AI — it exceeds the %s size limit."
  },
  {
    "id": "agents.skipped_images_multiple",
    "translation": "Note: %d images were not sent to the AI — they exceed the %s size limit: %s"
  }
```

- [ ] **Step 2: Add entries to `i18n/es.json`**

Append before the closing `]`:

```json
  ,{
    "id": "agents.skipped_image_single",
    "translation": "Nota: La imagen \"%s\" (%s) no se envió a la IA — supera el límite de tamaño de %s."
  },
  {
    "id": "agents.skipped_images_multiple",
    "translation": "Nota: %d imágenes no se enviaron a la IA — superan el límite de tamaño de %s: %s"
  }
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./i18n/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add i18n/en.json i18n/es.json
git commit -m "feat(i18n): add skipped image size limit translations"
```

---

## Task 3: Implement `buildSkippedImagesNote` with TDD

**Files:**
- Create: `conversations/post_to_ai_post_test.go`
- Modify: `conversations/conversations.go`

- [ ] **Step 1: Write the failing tests**

Create `conversations/post_to_ai_post_test.go`:

```go
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
		name     string
		skipped  []llm.SkippedFile
		contains []string
	}{
		{
			name: "single skipped image",
			skipped: []llm.SkippedFile{
				{Name: "photo.jpg", Size: 8 * 1024 * 1024, Limit: 5 * 1024 * 1024},
			},
			contains: []string{"photo.jpg", "8.0 MB", "5 MB"},
		},
		{
			name: "multiple skipped images",
			skipped: []llm.SkippedFile{
				{Name: "a.jpg", Size: 6 * 1024 * 1024, Limit: 5 * 1024 * 1024},
				{Name: "b.png", Size: 9 * 1024 * 1024, Limit: 5 * 1024 * 1024},
			},
			contains: []string{"2", "a.jpg", "b.png", "5 MB"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			note := buildSkippedImagesNote(T, tc.skipped)
			for _, substr := range tc.contains {
				require.Contains(t, note, substr)
			}
		})
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

```bash
go test ./conversations/... -run TestBuildSkippedImagesNote -v
```

Expected: compilation error — `buildSkippedImagesNote undefined`.

- [ ] **Step 3: Implement `buildSkippedImagesNote` in `conversations/conversations.go`**

Add this function anywhere in `conversations/conversations.go` (e.g., after `isImageMimeType`):

```go
func buildSkippedImagesNote(T i18n.TranslationFunc, skipped []llm.SkippedFile) string {
	limitMB := fmt.Sprintf("%.0f MB", float64(skipped[0].Limit)/(1024*1024))
	if len(skipped) == 1 {
		sizeMB := fmt.Sprintf("%.1f MB", float64(skipped[0].Size)/(1024*1024))
		return T(
			"agents.skipped_image_single",
			"Note: The image \"%s\" (%s) was not sent to the AI — it exceeds the %s size limit.",
			skipped[0].Name, sizeMB, limitMB,
		)
	}
	names := make([]string, len(skipped))
	for i, f := range skipped {
		names[i] = fmt.Sprintf("%s (%.1f MB)", f.Name, float64(f.Size)/(1024*1024))
	}
	return T(
		"agents.skipped_images_multiple",
		"Note: %d images were not sent to the AI — they exceed the %s size limit: %s",
		len(skipped), limitMB, strings.Join(names, ", "),
	)
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./conversations/... -run TestBuildSkippedImagesNote -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add conversations/conversations.go conversations/post_to_ai_post_test.go
git commit -m "feat(conversations): add buildSkippedImagesNote helper"
```

---

## Task 4: Update `PostToAIPost` to skip oversized images

**Files:**
- Modify: `conversations/tool_handling_test.go` (extend `fakeMMClient`)
- Modify: `conversations/post_to_ai_post_test.go` (add `PostToAIPost` tests)
- Modify: `conversations/conversations.go` (the actual change)

- [ ] **Step 1: Extend `fakeMMClient` in `conversations/tool_handling_test.go`**

Find the `fakeMMClient` struct (around line 31) and add two fields:

```go
type fakeMMClient struct {
	users        map[string]*model.User
	postThreads  map[string]*model.PostList
	kv           map[string]interface{}
	updatedPosts []*model.Post
	createdPosts []*model.Post
	kvDeletes    []string
	posts        map[string]*model.Post
	channels     map[string]*model.Channel
	fileInfos    map[string]*model.FileInfo   // ← add this
	fileContents map[string]io.ReadCloser      // ← add this
}
```

Update `GetFileInfo` (around line 186) and `GetFile` (around line 190) to use the maps:

```go
func (c *fakeMMClient) GetFileInfo(fileID string) (*model.FileInfo, error) {
	if c.fileInfos != nil {
		if fi, ok := c.fileInfos[fileID]; ok {
			return fi, nil
		}
	}
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) GetFile(fileID string) (io.ReadCloser, error) {
	if c.fileContents != nil {
		if f, ok := c.fileContents[fileID]; ok {
			return f, nil
		}
	}
	return nil, errors.New("not implemented")
}
```

- [ ] **Step 2: Write failing tests for `PostToAIPost` in `conversations/post_to_ai_post_test.go`**

Append to the existing file (keep package `conversations`). Note: since this is the `conversations` package (internal test), you cannot use `conversations.New` — you construct `Conversations` directly. Instead, use `conversations_test` package but be careful: `buildSkippedImagesNote` is unexported. Keep tests for `buildSkippedImagesNote` in `package conversations` and add a new file for `PostToAIPost` in `package conversations_test`.

Create a separate file `conversations/post_to_ai_post_integration_test.go` in `package conversations_test`:

```go
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
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

func makePostToAIPostConversations(t *testing.T, mmClient *fakeMMClient) (*conversations.Conversations, *bots.MMBots) {
	t.Helper()
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)
	botService := bots.New("p2lab-agents", mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
	c := conversations.New(nil, mmClient, nil, nil, botService, nil, licenseChecker, i18n.Init(), nil, nil)
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
		name            string
		fileSize        int64
		enableVision    bool
		wantFiles       int
		wantSkipped     int
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
```

- [ ] **Step 3: Run to confirm tests fail**

```bash
go test ./conversations/... -run TestPostToAIPostSkipsOversizedImage -v
```

Expected: FAIL — `result.SkippedFiles` is always empty; oversized image ends up in `Files` (or errors on `GetFile`).

- [ ] **Step 4: Implement the change in `conversations/conversations.go:PostToAIPost`**

In `PostToAIPost`, add `var skippedFiles []llm.SkippedFile` alongside `var filesForUpstream []llm.File` at the top of the function (around line 436):

```go
func (c *Conversations) PostToAIPost(bot *bots.Bot, post *model.Post) llm.Post {
	var filesForUpstream []llm.File
	var skippedFiles []llm.SkippedFile      // ← add this line
	message := format.PostBody(post)
	...
```

Find the vision block (around line 478):

```go
if bot.GetConfig().EnableVision && isImageMimeType(fileInfo.MimeType) {
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
}
```

Replace with:

```go
if bot.GetConfig().EnableVision && isImageMimeType(fileInfo.MimeType) {
	if fileInfo.Size > maxFileSize {
		skippedFiles = append(skippedFiles, llm.SkippedFile{
			Name:  fileInfo.Name,
			Size:  fileInfo.Size,
			Limit: maxFileSize,
		})
		continue
	}
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
}
```

Find the return statement at the bottom of `PostToAIPost` (around line 536) and add `SkippedFiles`:

```go
return llm.Post{
	Role:               role,
	Message:            message,
	Files:              filesForUpstream,
	SkippedFiles:       skippedFiles,        // ← add this line
	ToolUse:            tools,
	Reasoning:          reasoning,
	ReasoningSignature: reasoningSignature,
}
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./conversations/... -run TestPostToAIPostSkipsOversizedImage -v
```

Expected: PASS.

- [ ] **Step 6: Run full test suite to check no regressions**

```bash
go test ./conversations/...
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add conversations/conversations.go conversations/tool_handling_test.go conversations/post_to_ai_post_integration_test.go
git commit -m "feat(conversations): skip images exceeding MaxFileSize in PostToAIPost"
```

---

## Task 5: Wire `PostfixMessage` in `ProcessUserRequestWithContext`

**Files:**
- Modify: `conversations/conversations.go`

- [ ] **Step 1: Capture current post and set PostfixMessage**

In `conversations/conversations.go:ProcessUserRequestWithContext`, find line 173:

```go
posts = append(posts, c.PostToAIPost(bot, post))
```

Change to capture the current post:

```go
posts = append(posts, c.PostToAIPost(bot, post))
currentPost := posts[len(posts)-1]
```

Find the line after the nil-check of `result` from `ChatCompletion` (around line 192-195):

```go
result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
if err != nil {
	return nil, err
}
```

Add the postfix wiring immediately after:

```go
result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
if err != nil {
	return nil, err
}

if len(currentPost.SkippedFiles) > 0 {
	T := i18n.LocalizerFunc(c.i18n, postingUser.Locale)
	result.PostfixMessage = buildSkippedImagesNote(T, currentPost.SkippedFiles)
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./conversations/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add conversations/conversations.go
git commit -m "feat(conversations): set PostfixMessage on stream result for skipped images"
```

---

## Task 6: Append `PostfixMessage` in `StreamToPost`

**Files:**
- Create: `streaming/postfix_message_test.go`
- Modify: `streaming/streaming.go`

- [ ] **Step 1: Write failing test**

Create `streaming/postfix_message_test.go`:

```go
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
		name           string
		llmResponse    string
		postfixMessage string
		wantSuffix     string
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
			service := New(client, bundle)

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
```

- [ ] **Step 2: Run to confirm it fails**

```bash
go test ./streaming/... -run TestStreamToPostAppendsPostfixMessage -v
```

Expected: FAIL — no separator or postfix in message.

- [ ] **Step 3: Implement the change in `streaming/streaming.go`**

Find the `EventTypeEnd` case in `StreamToPost` (around line 444). The current code is:

```go
case llm.EventTypeEnd:
	// Stream has closed cleanly
	if strings.TrimSpace(post.Message) == "" {
		p.mmClient.LogError("LLM closed stream with no result")
		T := i18n.LocalizerFunc(p.i18n, userLocale)
		post.Message = T("agents.stream_to_post_llm_not_return", "Sorry! The LLM did not return a result.")
		p.sendPostStreamingUpdateEventWithBroadcast(post, post.Message, broadcast)
	}

	// ... (reasoning prop logging) ...

	if err := p.mmClient.UpdatePost(post); err != nil {
		p.mmClient.LogError("Streaming failed to update post", "error", err)
		return
	}
	return
```

Add the postfix append AFTER the empty-message check but BEFORE `UpdatePost`:

```go
case llm.EventTypeEnd:
	// Stream has closed cleanly
	if strings.TrimSpace(post.Message) == "" {
		p.mmClient.LogError("LLM closed stream with no result")
		T := i18n.LocalizerFunc(p.i18n, userLocale)
		post.Message = T("agents.stream_to_post_llm_not_return", "Sorry! The LLM did not return a result.")
		p.sendPostStreamingUpdateEventWithBroadcast(post, post.Message, broadcast)
	}

	// Append static postfix note (e.g. skipped images notice)
	if stream.PostfixMessage != "" {
		post.Message += "\n\n---\n" + stream.PostfixMessage
		p.sendPostStreamingUpdateEventWithBroadcast(post, post.Message, broadcast)
	}

	// Inline citations have already been cleaned in EventTypeAnnotations handler
	// (if there were any citations, they were cleaned before annotations were sent)

	// Update post with all accumulated data
	// This includes the message and any reasoning that was added to props in EventTypeReasoningEnd
	if reasoningProp := post.GetProp(ReasoningSummaryProp); reasoningProp != nil {
		p.mmClient.LogDebug("Persisting post with reasoning summary", "post_id", post.Id)
	}
	if err := p.mmClient.UpdatePost(post); err != nil {
		p.mmClient.LogError("Streaming failed to update post", "error", err)
		return
	}
	return
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
go test ./streaming/... -run TestStreamToPostAppendsPostfixMessage -v
```

Expected: PASS.

- [ ] **Step 5: Run full streaming tests to check no regressions**

```bash
go test ./streaming/...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add streaming/streaming.go streaming/postfix_message_test.go
git commit -m "feat(streaming): append PostfixMessage to bot reply at stream end"
```

---

## Task 7: Run full test suite and deploy

- [ ] **Step 1: Run all tests**

```bash
make test
```

Expected: all tests pass.

- [ ] **Step 2: Deploy to staging**

```bash
make deploy
```

- [ ] **Step 3: Manual verification**

1. Send a message with an image > 5 MB attached to the bot.
   - Expected bot reply: LLM response followed by `---` and `Note: The image "filename.jpg" (X.X MB) was not sent to the AI — it exceeds the 5 MB size limit.`
2. Send a message with an image ≤ 5 MB attached.
   - Expected: bot processes the image normally, no note appended.
3. Send a message with two images both > 5 MB.
   - Expected: `Note: 2 images were not sent to the AI — they exceed the 5 MB size limit: a.jpg (X.X MB), b.png (X.X MB)`

---

## Implementation Notes

- **`maxFileSize` scoping:** The variable is already declared in `PostToAIPost` at line 440 as a local: `maxFileSize := defaultMaxFileSize` (with optional override from `bot.GetConfig().MaxFileSize`). No scoping issue.
- **`PostfixMessage` pointer safety:** `ChatCompletion` returns `*TextStreamResult`. Setting `result.PostfixMessage` modifies the pointed-to struct before it's passed to `streamResponseToExistingPost` → `StreamToPost`. No copy issues.
- **Historical thread posts:** Only the CURRENT triggering post's `SkippedFiles` generate the note. Thread history posts have their skipped files silently discarded — intentional.
- **Package layout for tests:** `buildSkippedImagesNote` is unexported → its test lives in `package conversations` (internal). `PostToAIPost` tests live in `package conversations_test` (external) and use `conversations.New(...)`.
- **`bots.NewBot` signature:** `bots.NewBot(config llm.BotConfig, serviceConfig llm.ServiceConfig, mmBot *model.Bot, llmModel llm.LanguageModel) *bots.Bot` — pass `nil` for `llmModel` in tests.
