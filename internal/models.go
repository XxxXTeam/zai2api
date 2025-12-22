package internal

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var BaseModelMapping = map[string]string{
	"GLM-4.5":      "0727-360B-API",
	"GLM-4.6":      "GLM-4-6-API-V1",
	"GLM-4.7":      "glm-4.7",
	"GLM-4.5-V":    "glm-4.5v",
	"GLM-4.6-V":    "glm-4.6v",
	"GLM-4.5-Air":  "0727-106B-API",
	"0808-360B-DR": "0808-360B-DR",
}
var ModelList = []string{
	"GLM-4.5",
	"GLM-4.6",
	"GLM-4.7",
	"GLM-4.5-thinking",
	"GLM-4.6-thinking",
	"GLM-4.7-thinking",
	"GLM-4.5-V",
	"GLM-4.6-V",
	"GLM-4.6-V-thinking",
	"GLM-4.5-Air",
	"0808-360B-DR",
}

func ParseModelName(model string) (baseModel string, enableThinking bool, enableSearch bool) {
	enableThinking = false
	enableSearch = false
	baseModel = model
	for {
		if strings.HasSuffix(baseModel, "-thinking") {
			enableThinking = true
			baseModel = strings.TrimSuffix(baseModel, "-thinking")
		} else if strings.HasSuffix(baseModel, "-search") {
			enableSearch = true
			baseModel = strings.TrimSuffix(baseModel, "-search")
		} else {
			break
		}
	}

	return baseModel, enableThinking, enableSearch
}

func IsThinkingModel(model string) bool {
	_, enableThinking, _ := ParseModelName(model)
	return enableThinking
}

func IsSearchModel(model string) bool {
	_, _, enableSearch := ParseModelName(model)
	return enableSearch
}

func GetTargetModel(model string) string {
	baseModel, _, _ := ParseModelName(model)
	if target, ok := BaseModelMapping[baseModel]; ok {
		return target
	}
	return model
}

func IsValidModel(model string) bool {
	baseModel, _, _ := ParseModelName(model)
	if _, ok := BaseModelMapping[baseModel]; ok {
		return true
	}
	if GetUpstreamConfig(model) != nil {
		return true
	}
	return false
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
}
type MediaURL struct {
	URL string `json:"url"`
}

type ImageURL = MediaURL
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string 或 []ContentPart
}

func (m *Message) ParseContent() (text string, imageURLs []string) {
	_, imageURLs, _ = m.ParseContentFull()
	text, _, _ = m.ParseContentFull()
	return text, imageURLs
}

func (m *Message) ParseContentFull() (text string, imageURLs []string, videoURLs []string) {
	switch content := m.Content.(type) {
	case string:
		return content, nil, nil
	case []interface{}:
		for _, item := range content {
			if part, ok := item.(map[string]interface{}); ok {
				partType, _ := part["type"].(string)
				switch partType {
				case "text":
					if t, ok := part["text"].(string); ok {
						text += t
					}
				case "image_url":
					if imgURL, ok := part["image_url"].(map[string]interface{}); ok {
						if url, ok := imgURL["url"].(string); ok {
							imageURLs = append(imageURLs, url)
						}
					}
				case "video_url":
					if vidURL, ok := part["video_url"].(map[string]interface{}); ok {
						if url, ok := vidURL["url"].(string); ok {
							videoURLs = append(videoURLs, url)
						}
					}
				}
			}
		}
	}
	return text, imageURLs, videoURLs
}
func (m *Message) ToUpstreamMessage(urlToFileID map[string]string) map[string]interface{} {
	text, imageURLs, videoURLs := m.ParseContentFull()
	LogDebug("[ToUpstreamMessage] role=%s, images=%d, videos=%d, mapKeys=%d", m.Role, len(imageURLs), len(videoURLs), len(urlToFileID))
	if len(imageURLs) == 0 && len(videoURLs) == 0 {
		return map[string]interface{}{
			"role":    m.Role,
			"content": text,
		}
	}

	var content []interface{}
	for _, imgURL := range imageURLs {
		urlPreview := imgURL
		if len(urlPreview) > 60 {
			urlPreview = urlPreview[:60] + "..."
		}
		if fileID, ok := urlToFileID[imgURL]; ok {
			LogDebug("[ToUpstreamMessage] Image MATCHED: %s -> %s", urlPreview, fileID)
			content = append(content, map[string]interface{}{
				"type": "image_url",
				"image_url": map[string]interface{}{
					"url": fileID,
				},
			})
		} else {
			LogDebug("[ToUpstreamMessage] Image NOT matched: %s", urlPreview)
		}
	}
	for _, vidURL := range videoURLs {
		urlPreview := vidURL
		if len(urlPreview) > 60 {
			urlPreview = urlPreview[:60] + "..."
		}
		if fileID, ok := urlToFileID[vidURL]; ok {
			LogDebug("[ToUpstreamMessage] Video MATCHED: %s -> %s", urlPreview, fileID)
			content = append(content, map[string]interface{}{
				"type": "video_url",
				"video_url": map[string]interface{}{
					"url": fileID,
				},
			})
		} else {
			LogDebug("[ToUpstreamMessage] Video NOT matched: %s", urlPreview)
		}
	}
	if text != "" {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": text,
		})
	}
	if len(content) == 0 || (len(content) == 1 && text != "") {
		return map[string]interface{}{
			"role":    m.Role,
			"content": text,
		}
	}
	return map[string]interface{}{
		"role":    m.Role,
		"content": content,
	}
}

type ChatRequest struct {
	Model            string      `json:"model"`
	Messages         []Message   `json:"messages"`
	Stream           bool        `json:"stream"`
	Tools            []Tool      `json:"tools,omitempty"`
	ToolChoice       interface{} `json:"tool_choice,omitempty"`
	Temperature      *float64    `json:"temperature,omitempty"`
	TopP             *float64    `json:"top_p,omitempty"`
	MaxTokens        *int        `json:"max_tokens,omitempty"`
	PresencePenalty  *float64    `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64    `json:"frequency_penalty,omitempty"`
	Stop             interface{} `json:"stop,omitempty"`
	User             string      `json:"user,omitempty"`
	StreamOptions    *struct {
		IncludeUsage bool `json:"include_usage,omitempty"`
	} `json:"stream_options,omitempty"`
}

type ChatCompletionChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Index        int          `json:"index"`
	Delta        *Delta       `json:"delta,omitempty"`
	Message      *MessageResp `json:"message,omitempty"`
	FinishReason *string      `json:"finish_reason"`
	Logprobs     interface{}  `json:"logprobs"`
}

type Delta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type MessageResp struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

type ChatCompletionChunkResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

var searchRefPattern = regexp.MustCompile(`【turn\d+search(\d+)】`)
var searchRefPrefixPattern = regexp.MustCompile(`【(t(u(r(n(\d+(s(e(a(r(c(h(\d+)?)?)?)?)?)?)?)?)?)?)?)?$`)

type SearchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Index int    `json:"index"`
	RefID string `json:"ref_id"`
}

type SearchRefFilter struct {
	buffer        string
	searchResults map[string]SearchResult
}

func NewSearchRefFilter() *SearchRefFilter {
	return &SearchRefFilter{
		searchResults: make(map[string]SearchResult),
	}
}

func (f *SearchRefFilter) AddSearchResults(results []SearchResult) {
	for _, r := range results {
		f.searchResults[r.RefID] = r
	}
}

func escapeMarkdownTitle(title string) string {
	title = strings.ReplaceAll(title, `\`, `\\`)
	title = strings.ReplaceAll(title, `[`, `\[`)
	title = strings.ReplaceAll(title, `]`, `\]`)
	return title
}

func (f *SearchRefFilter) Process(content string) string {
	content = f.buffer + content
	f.buffer = ""

	if content == "" {
		return ""
	}

	content = searchRefPattern.ReplaceAllStringFunc(content, func(match string) string {
		runes := []rune(match)
		refID := string(runes[1 : len(runes)-1])
		if result, ok := f.searchResults[refID]; ok {
			return fmt.Sprintf(`[\[%d\]](%s)`, result.Index, result.URL)
		}
		return ""
	})

	if content == "" {
		return ""
	}

	maxPrefixLen := 20
	if len(content) < maxPrefixLen {
		maxPrefixLen = len(content)
	}

	for i := 1; i <= maxPrefixLen; i++ {
		suffix := content[len(content)-i:]
		if searchRefPrefixPattern.MatchString(suffix) {
			f.buffer = suffix
			return content[:len(content)-i]
		}
	}

	return content
}

func (f *SearchRefFilter) Flush() string {
	result := f.buffer
	f.buffer = ""
	if result != "" {
		result = searchRefPattern.ReplaceAllStringFunc(result, func(match string) string {
			runes := []rune(match)
			refID := string(runes[1 : len(runes)-1])
			if r, ok := f.searchResults[refID]; ok {
				return fmt.Sprintf(`[\[%d\]](%s)`, r.Index, r.URL)
			}
			return ""
		})
	}
	return result
}

func (f *SearchRefFilter) GetSearchResultsMarkdown() string {
	if len(f.searchResults) == 0 {
		return ""
	}

	var results []SearchResult
	for _, r := range f.searchResults {
		results = append(results, r)
	}
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Index > results[j].Index {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	var sb strings.Builder
	for _, r := range results {
		escapedTitle := escapeMarkdownTitle(r.Title)
		sb.WriteString(fmt.Sprintf("[\\[%d\\] %s](%s)\n", r.Index, escapedTitle, r.URL))
	}
	sb.WriteString("\n")
	return sb.String()
}

func IsSearchResultContent(editContent string) bool {
	return strings.Contains(editContent, `"search_result"`)
}

func ParseSearchResults(editContent string) []SearchResult {
	searchResultKey := `"search_result":`
	idx := strings.Index(editContent, searchResultKey)
	if idx == -1 {
		return nil
	}

	startIdx := idx + len(searchResultKey)
	for startIdx < len(editContent) && editContent[startIdx] != '[' {
		startIdx++
	}
	if startIdx >= len(editContent) {
		return nil
	}

	bracketCount := 0
	endIdx := startIdx
	for endIdx < len(editContent) {
		if editContent[endIdx] == '[' {
			bracketCount++
		} else if editContent[endIdx] == ']' {
			bracketCount--
			if bracketCount == 0 {
				endIdx++
				break
			}
		}
		endIdx++
	}

	if bracketCount != 0 {
		return nil
	}

	jsonStr := editContent[startIdx:endIdx]
	var rawResults []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Index int    `json:"index"`
		RefID string `json:"ref_id"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &rawResults); err != nil {
		return nil
	}

	var results []SearchResult
	for _, r := range rawResults {
		results = append(results, SearchResult{
			Title: r.Title,
			URL:   r.URL,
			Index: r.Index,
			RefID: r.RefID,
		})
	}

	return results
}

func IsSearchToolCall(editContent string, phase string) bool {
	if phase != "tool_call" {
		return false
	}
	// tool_call 阶段包含 mcp 相关内容的都跳过
	return strings.Contains(editContent, `"mcp"`) || strings.Contains(editContent, `mcp-server`)
}

type ImageSearchResult struct {
	Title     string `json:"title"`
	Link      string `json:"link"`
	Thumbnail string `json:"thumbnail"`
}

func ParseImageSearchResults(editContent string) []ImageSearchResult {
	resultKey := `"result":`
	idx := strings.Index(editContent, resultKey)
	if idx == -1 {
		return nil
	}

	startIdx := idx + len(resultKey)
	for startIdx < len(editContent) && editContent[startIdx] != '[' {
		startIdx++
	}
	if startIdx >= len(editContent) {
		return nil
	}

	bracketCount := 0
	endIdx := startIdx
	inString := false
	escapeNext := false
	for endIdx < len(editContent) {
		ch := editContent[endIdx]

		if escapeNext {
			escapeNext = false
			endIdx++
			continue
		}

		if ch == '\\' {
			escapeNext = true
			endIdx++
			continue
		}

		if ch == '"' {
			inString = !inString
		}

		if !inString {
			if ch == '[' || ch == '{' {
				bracketCount++
			} else if ch == ']' || ch == '}' {
				bracketCount--
				if bracketCount == 0 && ch == ']' {
					endIdx++
					break
				}
			}
		}
		endIdx++
	}

	if bracketCount != 0 {
		return nil
	}

	jsonStr := editContent[startIdx:endIdx]

	var rawResults []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawResults); err != nil {
		return nil
	}

	var results []ImageSearchResult
	for _, item := range rawResults {
		if itemType, ok := item["type"].(string); ok && itemType == "text" {
			if text, ok := item["text"].(string); ok {
				result := parseImageSearchText(text)
				if result.Title != "" && result.Link != "" {
					results = append(results, result)
				}
			}
		}
	}

	return results
}

func parseImageSearchText(text string) ImageSearchResult {
	result := ImageSearchResult{}

	if titleIdx := strings.Index(text, "Title: "); titleIdx != -1 {
		titleStart := titleIdx + len("Title: ")
		titleEnd := strings.Index(text[titleStart:], ";")
		if titleEnd != -1 {
			result.Title = strings.TrimSpace(text[titleStart : titleStart+titleEnd])
		}
	}

	if linkIdx := strings.Index(text, "Link: "); linkIdx != -1 {
		linkStart := linkIdx + len("Link: ")
		linkEnd := strings.Index(text[linkStart:], ";")
		if linkEnd != -1 {
			result.Link = strings.TrimSpace(text[linkStart : linkStart+linkEnd])
		} else {
			result.Link = strings.TrimSpace(text[linkStart:])
		}
	}

	if thumbnailIdx := strings.Index(text, "Thumbnail: "); thumbnailIdx != -1 {
		thumbnailStart := thumbnailIdx + len("Thumbnail: ")
		result.Thumbnail = strings.TrimSpace(text[thumbnailStart:])
	}

	return result
}

func FormatImageSearchResults(results []ImageSearchResult) string {
	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, r := range results {
		escapedTitle := strings.ReplaceAll(r.Title, `[`, `\[`)
		escapedTitle = strings.ReplaceAll(escapedTitle, `]`, `\]`)
		sb.WriteString(fmt.Sprintf("\n![%s](%s)", escapedTitle, r.Link))
	}
	sb.WriteString("\n")
	return sb.String()
}

func ExtractTextBeforeGlmBlock(editContent string) string {
	if idx := strings.Index(editContent, "<glm_block"); idx != -1 {
		text := editContent[:idx]
		if strings.HasSuffix(text, "\n") {
			text = text[:len(text)-1]
		}
		return text
	}
	return ""
}
