# File Validation & User Feedback Layer

## Problem

When files are attached to thread posts, the plugin silently drops files it cannot process. Videos, unsupported image formats, and oversized images are ignored without any user-facing feedback. Each LLM provider has different rules about supported file types and size limits, but validation is inconsistent — OpenAI checks sizes, Anthropic and Bedrock do not.

Users experience this as the bot "ignoring" their attachments with no explanation.

## Decision

Add early validation in `PostToAIPost` using a `FileConstraints` config struct that each LLM provider declares. Files that fail validation are collected and a structured note is appended to the post message so the LLM naturally communicates the limitation to the user.

## Design

### FileConstraints Struct

New type in `llm/language_model.go`:

```go
type FileConstraints struct {
    SupportedImageTypes []string // e.g. ["image/jpeg", "image/png", "image/gif", "image/webp"]
    MaxImageSize        int64    // bytes, 0 = no plugin-side limit
    MaxTextFileSize     int64    // bytes, 0 = use defaultMaxFileSize (5MB)
}
```

New method on `LanguageModel` interface:

```go
FileConstraints() FileConstraints
```

### Provider Implementations

| Provider | SupportedImageTypes | MaxImageSize | MaxTextFileSize |
|---|---|---|---|
| Anthropic | jpeg, png, gif, webp | 5MB | 0 (default) |
| OpenAI | jpeg, png, gif, webp | 20MB | 0 (default) |
| Bedrock | jpeg, png, gif, webp | 5MB | 0 (default) |

### Validation Flow in PostToAIPost

For each file in `post.FileIds`:

1. Fetch `fileInfo` from Mattermost API
2. Check against constraints:
   - Has server-extracted text (`fileInfo.Content` non-empty)? Process as text (unchanged)
   - Is `text/*` MIME type? Process as text with size limit from constraints or default (unchanged)
   - Is image + `EnableVision` enabled + MIME in `SupportedImageTypes` + size within `MaxImageSize`? Add to `filesForUpstream`
   - Everything else: collect into `skippedFiles` with a human-readable reason
3. If `skippedFiles` is non-empty, append structured note to message

### Reason Categories

| Condition | Reason |
|---|---|
| Image MIME but not in supported list | "unsupported image format, supported: JPEG, PNG, GIF, WebP" |
| Supported image type but exceeds size limit | "image too large: 12MB, maximum: 5MB" |
| Image but vision is disabled for this bot | "image processing is not enabled" |
| Any other unsupported MIME type | "file type not supported" |

### Note Format

Appended to the post message when there are skipped files:

```
[Note: The following attached files could not be processed:
- meeting.mp4 (video files are not supported)
- photo.tiff (unsupported image format, supported: JPEG, PNG, GIF, WebP)
- huge_screenshot.png (image too large: 12MB, maximum: 5MB)
- archive.zip (file type not supported)
Only text files and images (JPEG, PNG, GIF, WebP) can be processed.]
```

### Wrapper Delegation

All `LanguageModel` wrappers (`TruncationWrapper`, `TokenUsageLoggingWrapper`, `LanguageModelLogWrapper`, `LanguageModelTestLogWrapper`) delegate `FileConstraints()` to their wrapped model.

### Provider Cleanup

After validation moves to `PostToAIPost`, providers can trust that files in `llm.File` are already valid:
- `anthropic/anthropic.go`: remove `isValidImageType` check from `convertFilesToBlocks`
- `openai/openai.go`: remove MIME type and size checks from `postsToChatCompletionMessages`
- `bedrock/bedrock.go`: remove `isValidImageType` check from image block construction

Keep defensive error handling for `io.ReadAll` failures (network issues reading from store).

## Files Changed

| File | Change |
|---|---|
| `llm/language_model.go` | Add `FileConstraints` struct and `FileConstraints()` to `LanguageModel` interface |
| `anthropic/anthropic.go` | Implement `FileConstraints()`, simplify `convertFilesToBlocks` |
| `openai/openai.go` | Implement `FileConstraints()`, simplify image handling in `postsToChatCompletionMessages` |
| `bedrock/bedrock.go` | Implement `FileConstraints()`, simplify image block construction |
| `conversations/conversations.go` | Rewrite file loop in `PostToAIPost` with early validation and note generation |
| `llm/truncation.go` | Delegate `FileConstraints()` |
| `llm/logging.go` | Delegate `FileConstraints()` |
| `llm/token_tracking.go` | Delegate `FileConstraints()` |
| Tests | Unit tests for validation logic, constraint delegation, note formatting |

## What Doesn't Change

- `llm.File` struct
- Thread fetching logic
- Thread analysis path (`threads.go`) — uses text serialization, not files
- `BotConfig.MaxFileSize` — still used as fallback when `FileConstraints.MaxTextFileSize` is 0
- `BotConfig.EnableVision` — still checked before processing images

## Future Work

- Add support for additional file types (video transcription, PDF processing) incrementally
- Provider capability abstraction (Approach B from original analysis) if constraints grow complex
- File pre-processing pipeline (resize images, extract audio) as a separate feature
