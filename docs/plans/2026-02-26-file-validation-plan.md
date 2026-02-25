# File Validation & User Feedback Layer — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add early file validation in `PostToAIPost` so unsupported/oversized files produce a structured note instead of being silently dropped.

**Architecture:** Each LLM provider declares a `FileConstraints` struct (supported image types, max sizes). `PostToAIPost` checks every attached file against these constraints before fetching file content. Unsupported files are collected into a note appended to the post message. Provider-side redundant validation is then removed.

**Tech Stack:** Go 1.23+, Mattermost plugin API, mockery v3 for mock generation.

---

### Task 1: Add FileConstraints to the llm package

**Files:**
- Modify: `llm/language_model.go:27-33` (add struct + interface method)

**Step 1: Add the `FileConstraints` struct and `HasSupportedImageType` helper**

Add after the `LanguageModel` interface definition (after line 33) in `llm/language_model.go`:

```go
// FileConstraints describes the file types and sizes a provider supports.
type FileConstraints struct {
	SupportedImageTypes []string // MIME types, e.g. ["image/jpeg", "image/png"]
	MaxImageSize        int64    // bytes, 0 = no plugin-side limit
	MaxTextFileSize     int64    // bytes, 0 = use defaultMaxFileSize (5MB)
}

// HasSupportedImageType checks if the given MIME type is in the supported list.
func (fc FileConstraints) HasSupportedImageType(mimeType string) bool {
	return slices.Contains(fc.SupportedImageTypes, mimeType)
}
```

Add `"slices"` to the import block.

**Step 2: Add `FileConstraints()` to the `LanguageModel` interface**

Add to the interface at `llm/language_model.go:27-33`:

```go
type LanguageModel interface {
	ChatCompletion(conversation CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error)
	ChatCompletionNoStream(conversation CompletionRequest, opts ...LanguageModelOption) (string, error)

	CountTokens(text string) int
	InputTokenLimit() int
	FileConstraints() FileConstraints
}
```

**Step 3: Verify it doesn't compile**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go build ./llm/...`
Expected: Compiles (the llm package itself is fine — the failures will be in packages that implement the interface)

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go build ./...`
Expected: FAIL — providers and wrappers don't implement `FileConstraints()` yet

**Step 4: Commit**

```
feat(llm): add FileConstraints struct and interface method
```

---

### Task 2: Implement FileConstraints on all providers

**Files:**
- Modify: `anthropic/anthropic.go` (add method after `InputTokenLimit` around line 560)
- Modify: `openai/openai.go` (add method after `InputTokenLimit` around line 1290)
- Modify: `bedrock/bedrock.go` (add method after `InputTokenLimit` around line 615)

**Step 1: Implement on Anthropic**

Add to `anthropic/anthropic.go` after `InputTokenLimit()`:

```go
func (a *Anthropic) FileConstraints() llm.FileConstraints {
	return llm.FileConstraints{
		SupportedImageTypes: []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
		MaxImageSize:        5 * 1024 * 1024, // 5MB Anthropic API limit
	}
}
```

**Step 2: Implement on OpenAI**

Add to `openai/openai.go` after `InputTokenLimit()`:

```go
func (s *OpenAI) FileConstraints() llm.FileConstraints {
	return llm.FileConstraints{
		SupportedImageTypes: []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
		MaxImageSize:        OpenAIMaxImageSize, // 20MB
	}
}
```

**Step 3: Implement on Bedrock**

Add to `bedrock/bedrock.go` after `InputTokenLimit()`:

```go
func (b *Bedrock) FileConstraints() llm.FileConstraints {
	return llm.FileConstraints{
		SupportedImageTypes: []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
		MaxImageSize:        5 * 1024 * 1024, // 5MB conservative default for Bedrock
	}
}
```

**Step 4: Verify providers compile**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go build ./anthropic/... ./openai/... ./bedrock/...`
Expected: Compiles

**Step 5: Commit**

```
feat(providers): implement FileConstraints for Anthropic, OpenAI, Bedrock
```

---

### Task 3: Add FileConstraints delegation to all wrappers

**Files:**
- Modify: `llm/truncation.go` (add method after `InputTokenLimit`, line 42)
- Modify: `llm/logging.go` (add method after each wrapper's `InputTokenLimit`, lines 46 and 81)
- Modify: `llm/token_tracking.go` (add method after `InputTokenLimit`, line 163)

**Step 1: Add to TruncationWrapper**

Add to `llm/truncation.go` after `InputTokenLimit()` (line 42):

```go
func (w *TruncationWrapper) FileConstraints() FileConstraints {
	return w.wrapped.FileConstraints()
}
```

**Step 2: Add to LanguageModelLogWrapper**

Add to `llm/logging.go` after the first `InputTokenLimit()` (line 46):

```go
func (w *LanguageModelLogWrapper) FileConstraints() FileConstraints {
	return w.wrapped.FileConstraints()
}
```

**Step 3: Add to LanguageModelTestLogWrapper**

Add to `llm/logging.go` after the second `InputTokenLimit()` (line 81):

```go
func (w *LanguageModelTestLogWrapper) FileConstraints() FileConstraints {
	return w.wrapped.FileConstraints()
}
```

**Step 4: Add to TokenUsageLoggingWrapper**

Add to `llm/token_tracking.go` after `InputTokenLimit()` (line 163):

```go
func (w *TokenUsageLoggingWrapper) FileConstraints() FileConstraints {
	return w.wrapped.FileConstraints()
}
```

**Step 5: Fix the internal test mock**

Add to `llm/token_tracking_test.go` after `InputTokenLimit()` (line 38):

```go
func (m *MockLanguageModel) FileConstraints() FileConstraints {
	args := m.Called()
	return args.Get(0).(FileConstraints)
}
```

**Step 6: Verify the llm package compiles and tests pass**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go build ./llm/...`
Expected: Compiles

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go test ./llm/...`
Expected: PASS (existing tests should still work — they don't call `FileConstraints()`)

**Step 7: Commit**

```
feat(llm): add FileConstraints delegation to all LanguageModel wrappers
```

---

### Task 4: Regenerate the mock

**Files:**
- Regenerate: `llm/mocks/language_model_mock.go`

**Step 1: Regenerate mocks**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && make mock`
Expected: Mocks regenerated with `FileConstraints()` method added to `MockLanguageModel`

**Step 2: Verify the generated mock has FileConstraints**

Check that `llm/mocks/language_model_mock.go` now contains a `FileConstraints` method.

**Step 3: Verify everything compiles**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go build ./...`
Expected: Compiles — all types now satisfy the `LanguageModel` interface

**Step 4: Run full test suite**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && make test`
Expected: PASS

**Step 5: Commit**

```
chore: regenerate mocks for FileConstraints interface method
```

---

### Task 5: Write tests for the file validation logic

**Files:**
- Create: `conversations/file_validation_test.go`

**Step 1: Write the test file**

Create `conversations/file_validation_test.go` with table-driven tests covering all validation paths. The tests should exercise `PostToAIPost` with various file scenarios.

Since `PostToAIPost` requires a `*bots.Bot` with a working `LLM()` that returns `FileConstraints`, and a mock `mmClient`, use the existing test patterns from `conversations_test.go`.

```go
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
	"github.com/stretchr/testify/require"
)

func TestPostToAIPost_FileValidation(t *testing.T) {
	constraints := llm.FileConstraints{
		SupportedImageTypes: []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
		MaxImageSize:        5 * 1024 * 1024, // 5MB
	}

	tests := []struct {
		name           string
		fileInfos      []*model.FileInfo
		enableVision   bool
		expectFiles    int    // number of llm.File in result
		expectContains string // substring expected in result.Message
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
				// For text files and images, mock GetFile
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
```

**Step 2: Run the test to verify it fails**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go test -v ./conversations/ -run TestPostToAIPost_FileValidation`
Expected: FAIL — the current `PostToAIPost` doesn't call `FileConstraints()` or produce notes. Some test cases (unsupported format note, oversized note, video note) will fail because the current code silently passes or drops those files.

**Step 3: Commit**

```
test: add table-driven tests for file validation in PostToAIPost
```

---

### Task 6: Implement file validation in PostToAIPost

**Files:**
- Modify: `conversations/conversations.go:341-400` (rewrite `isImageMimeType` and the file loop in `PostToAIPost`)

**Step 1: Add the `formatSkippedFilesNote` helper and `humanReadableSize` helper**

Add before `PostToAIPost` in `conversations/conversations.go` (around line 341), replacing the old `isImageMimeType`:

```go
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
```

**Step 2: Rewrite the file loop in `PostToAIPost`**

Replace the file processing loop in `PostToAIPost` (lines 355-400 approximately) with:

```go
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
```

**Step 3: Remove the old `isImageMimeType` function**

Delete the `isImageMimeType` function at line 341-343. It's replaced by the constraint-based check.

**Step 4: Run the file validation tests**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go test -v ./conversations/ -run TestPostToAIPost_FileValidation`
Expected: PASS

**Step 5: Run the full test suite**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && make test`
Expected: PASS

**Step 6: Commit**

```
feat: add early file validation with user feedback in PostToAIPost

Files are now validated against provider-specific constraints before
being sent to the LLM. Unsupported files produce a structured note
in the message so the bot can inform the user.
```

---

### Task 7: Clean up provider-side redundant validation

**Files:**
- Modify: `anthropic/anthropic.go:142-159` (simplify `convertFilesToBlocks`)
- Modify: `openai/openai.go:276-310` (simplify image handling)
- Modify: `bedrock/bedrock.go:175-213` (simplify image block construction)

**Step 1: Simplify Anthropic `convertFilesToBlocks`**

In `anthropic/anthropic.go`, replace `convertFilesToBlocks` (lines 142-159). Since files are now pre-validated, remove the `isValidImageType` check. Keep the `io.ReadAll` error handling:

```go
func convertFilesToBlocks(files []llm.File) []anthropicSDK.ContentBlockParamUnion {
	var blocks []anthropicSDK.ContentBlockParamUnion
	for _, file := range files {
		data, err := io.ReadAll(file.Reader)
		if err != nil {
			blocks = append(blocks, anthropicSDK.NewTextBlock("[Error reading image data]"))
			continue
		}

		blocks = append(blocks, anthropicSDK.NewImageBlockBase64(file.MimeType, base64.StdEncoding.EncodeToString(data)))
	}
	return blocks
}
```

Remove the `isValidImageType` function from `anthropic/anthropic.go` (lines 64-71) — it's no longer used.

**Step 2: Simplify OpenAI image handling**

In `openai/openai.go`, simplify the image handling in `postsToChatCompletionMessages` (around lines 285-307). Remove the MIME type check and size check since files are pre-validated:

```go
			for _, file := range post.Files {
				fileBytes, err := io.ReadAll(file.Reader)
				if err != nil {
					continue
				}
				imageEncoded := base64.StdEncoding.EncodeToString(fileBytes)
				encodedString := fmt.Sprintf("data:"+file.MimeType+";base64,%s", imageEncoded)
				parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
					URL:    encodedString,
					Detail: "auto",
				}))
			}
```

Keep `OpenAIMaxImageSize` constant — it's referenced by `FileConstraints()`.

**Step 3: Simplify Bedrock image block construction**

In `bedrock/bedrock.go`, simplify the image handling (around lines 175-213). Remove `isValidImageType` check since files are pre-validated. Keep the `io.ReadAll` error handling:

```go
		for _, file := range post.Files {
			data, err := io.ReadAll(file.Reader)
			if err != nil {
				currentBlocks = append(currentBlocks, &types.ContentBlockMemberText{
					Value: "[Error reading image data]",
				})
				continue
			}

			var format types.ImageFormat
			switch file.MimeType {
			case "image/jpeg":
				format = types.ImageFormatJpeg
			case "image/png":
				format = types.ImageFormatPng
			case "image/gif":
				format = types.ImageFormatGif
			case "image/webp":
				format = types.ImageFormatWebp
			}

			imageBlock := &types.ContentBlockMemberImage{
				Value: types.ImageBlock{
					Format: format,
					Source: &types.ImageSourceMemberBytes{
						Value: data,
					},
				},
			}
			currentBlocks = append(currentBlocks, imageBlock)
		}
```

Remove the `isValidImageType` function from `bedrock/bedrock.go` (lines 118-127) — it's no longer used.

**Step 4: Verify everything compiles**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && go build ./...`
Expected: Compiles. If `isValidImageType` was referenced elsewhere, the compiler will tell us.

**Step 5: Run full test suite**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && make test`
Expected: PASS

**Step 6: Commit**

```
refactor(providers): remove redundant file validation from providers

File type and size validation now happens centrally in PostToAIPost.
Providers can trust that files in llm.File are pre-validated.
```

---

### Task 8: Run linting and full verification

**Files:** None (verification only)

**Step 1: Run linter**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && make check-style-fix`
Expected: PASS (or fix any issues found)

**Step 2: Run full test suite**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && make test`
Expected: PASS

**Step 3: Build and deploy**

Run: `cd /Users/sebastianpatinolang/code/p2lab/mattermost-plugin-agents && make deploy`
Expected: Plugin builds and deploys successfully
