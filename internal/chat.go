package internal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/corpix/uarand"
	"github.com/google/uuid"
)

// generateRandomIP 生成随机 IP 地址用于 X-Forwarded-For
func generateRandomIP() string {
	// 生成看起来合理的公网 IP
	// 避免保留地址段：10.x, 172.16-31.x, 192.168.x, 127.x
	firstOctet := []int{36, 42, 58, 60, 61, 101, 106, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 139, 140, 144, 150, 153, 157, 163, 171, 175, 180, 182, 183, 202, 210, 211, 218, 219, 220, 221, 222, 223}
	first := firstOctet[rand.Intn(len(firstOctet))]
	return fmt.Sprintf("%d.%d.%d.%d", first, rand.Intn(256), rand.Intn(256), rand.Intn(254)+1)
}

// APIError OpenAI 兼容的错误格式
type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ErrorResponse 错误响应结构
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// 错误类型常量
const (
	ErrTypeInvalidRequest = "invalid_request_error"
	ErrTypeAuthentication = "authentication_error"
	ErrTypeNotFound       = "not_found_error"
	ErrTypeServer         = "server_error"
	ErrTypeUpstream       = "upstream_error"
)

// writeError 写入错误响应
func writeError(w http.ResponseWriter, statusCode int, errType, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error: APIError{
			Message: message,
			Type:    errType,
			Code:    code,
		},
	})
}

// writeErrorResponse 统一错误响应（通用请求失败）
func writeErrorResponse(w http.ResponseWriter, statusCode int) {
	writeError(w, statusCode, ErrTypeServer, "请求失败", "")
}

// writeInvalidRequestError 无效请求错误
func writeInvalidRequestError(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, ErrTypeInvalidRequest, message, "invalid_request")
}

// writeModelNotFoundError 模型不存在错误
func writeModelNotFoundError(w http.ResponseWriter, model string) {
	writeError(w, http.StatusNotFound, ErrTypeNotFound,
		fmt.Sprintf("模型 '%s' 不存在", model), "model_not_found")
}

// writeUpstreamError 上游错误（透传）
func writeUpstreamError(w http.ResponseWriter, statusCode int, upstreamBody []byte) {
	// 尝试解析上游错误
	var upstreamErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
		Msg     string `json:"msg"`
	}

	message := "请求失败"
	if err := json.Unmarshal(upstreamBody, &upstreamErr); err == nil {
		if upstreamErr.Error.Message != "" {
			message = upstreamErr.Error.Message
		} else if upstreamErr.Message != "" {
			message = upstreamErr.Message
		} else if upstreamErr.Msg != "" {
			message = upstreamErr.Msg
		}
	}

	writeError(w, statusCode, ErrTypeUpstream, message, "upstream_error")
}

func extractLatestUserContent(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			text, _ := messages[i].ParseContent()
			return text
		}
	}
	return ""
}

func extractAllMediaURLs(messages []Message) (imageURLs, videoURLs []string) {
	for _, msg := range messages {
		_, imgs, vids := msg.ParseContentFull()
		imageURLs = append(imageURLs, imgs...)
		videoURLs = append(videoURLs, vids...)
	}
	return imageURLs, videoURLs
}

func makeUpstreamRequest(token string, messages []Message, model string, imageURLs, videoURLs []string) (*http.Response, string, error) {
	payload, err := DecodeJWTPayload(token)
	if err != nil || payload == nil {
		return nil, "", fmt.Errorf("invalid token")
	}

	userID := payload.ID
	chatID := uuid.New().String()
	timestamp := time.Now().UnixMilli()
	requestID := uuid.New().String()
	userMsgID := uuid.New().String()

	// 使用新的模型映射系统
	mapping := GetUpstreamConfig(model)
	var targetModel string
	var enableThinking, autoWebSearch bool
	var mcpServers []string

	if mapping != nil {
		targetModel = mapping.UpstreamModelID
		enableThinking = mapping.EnableThinking
		autoWebSearch = mapping.AutoWebSearch
		mcpServers = mapping.MCPServers
		LogDebug("Model mapping: %s -> %s (thinking=%v, search=%v)", model, targetModel, enableThinking, autoWebSearch)
	} else {
		// 回退到老的逻辑
		targetModel = GetTargetModel(model)
		enableThinking = IsThinkingModel(model)
		autoWebSearch = IsSearchModel(model)
		LogDebug("Using fallback model mapping: %s -> %s", model, targetModel)
	}

	if targetModel == "glm-4.5v" || targetModel == "glm-4.6v" {
		autoWebSearch = false
	}

	// 所有请求添加图片处理MCP服务器
	vlmServers := []string{"vlm-image-search", "vlm-image-recognition", "vlm-image-processing"}
	existingSet := make(map[string]bool)
	for _, s := range mcpServers {
		existingSet[s] = true
	}
	for _, s := range vlmServers {
		if !existingSet[s] {
			mcpServers = append(mcpServers, s)
		}
	}

	latestUserContent := extractLatestUserContent(messages)

	signature := GenerateSignature(userID, requestID, latestUserContent, timestamp)

	url := fmt.Sprintf("https://chat.z.ai/api/v2/chat/completions?timestamp=%d&requestId=%s&user_id=%s&version=0.0.1&platform=web&token=%s&current_url=%s&pathname=%s&signature_timestamp=%d",
		timestamp, requestID, userID, token,
		fmt.Sprintf("https://chat.z.ai/c/%s", chatID),
		fmt.Sprintf("/c/%s", chatID),
		timestamp)

	urlToFileID := make(map[string]string)
	var filesData []map[string]interface{}

	// 上传图片
	if len(imageURLs) > 0 {
		LogDebug("[Upstream] Uploading %d images...", len(imageURLs))
		imageFiles, _ := UploadImages(token, imageURLs)
		LogDebug("[Upstream] Image upload result: %d files", len(imageFiles))
		for i, f := range imageFiles {
			if i < len(imageURLs) {
				urlToFileID[imageURLs[i]] = f.ID
			}
			filesData = append(filesData, map[string]interface{}{
				"type":            f.Type,
				"file":            f.File,
				"id":              f.ID,
				"url":             f.URL,
				"name":            f.Name,
				"status":          f.Status,
				"size":            f.Size,
				"error":           f.Error,
				"itemId":          f.ItemID,
				"media":           f.Media,
				"ref_user_msg_id": userMsgID,
			})
		}
	}

	// 上传视频
	if len(videoURLs) > 0 {
		LogDebug("[Upstream] Uploading %d videos...", len(videoURLs))
		videoFiles, _ := UploadVideos(token, videoURLs)
		LogDebug("[Upstream] Video upload result: %d files", len(videoFiles))
		for i, f := range videoFiles {
			if i < len(videoURLs) {
				urlToFileID[videoURLs[i]] = f.ID
			}
			filesData = append(filesData, map[string]interface{}{
				"type":            f.Type,
				"file":            f.File,
				"id":              f.ID,
				"url":             f.URL,
				"name":            f.Name,
				"status":          f.Status,
				"size":            f.Size,
				"error":           f.Error,
				"itemId":          f.ItemID,
				"media":           f.Media,
				"ref_user_msg_id": userMsgID,
			})
		}
	}
	var upstreamMessages []map[string]interface{}
	for _, msg := range messages {
		upstreamMessages = append(upstreamMessages, msg.ToUpstreamMessage(urlToFileID))
	}

	body := map[string]interface{}{
		"stream":           true,
		"model":            targetModel,
		"messages":         upstreamMessages,
		"signature_prompt": latestUserContent,
		"params":           map[string]interface{}{},
		"features": map[string]interface{}{
			"image_generation": true,
			"web_search":       true,
			"auto_web_search":  autoWebSearch,
			"preview_mode":     true,
			"flags":            []string{},
			"enable_thinking":  enableThinking,
		},
		"chat_id": chatID,
		"id":      uuid.New().String(),
	}

	if len(mcpServers) > 0 {
		body["mcp_servers"] = mcpServers
	}

	if len(filesData) > 0 {
		body["files"] = filesData
		body["current_user_message_id"] = userMsgID
		LogDebug("[Upstream] Attaching %d files to request, userMsgID=%s", len(filesData), userMsgID)
		for i, fd := range filesData {
			LogDebug("[Upstream] File %d: id=%v, type=%v, name=%v, status=%v", i+1, fd["id"], fd["type"], fd["name"], fd["status"])
		}
	}

	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, "", err
	}

	randomIP := generateRandomIP()
	userAgent := uarand.GetRandom()

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-FE-Version", GetFeVersion())
	req.Header.Set("X-Signature", signature)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Origin", "https://chat.z.ai")
	req.Header.Set("Referer", fmt.Sprintf("https://chat.z.ai/c/%s", chatID))
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Forwarded-For", randomIP)
	req.Header.Set("X-Real-IP", randomIP)

	LogDebug("Upstream request: model=%s, messages=%d, XFF=%s", targetModel, len(messages), randomIP)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}

	LogDebug("Upstream response: status=%d, XFF=%s", resp.StatusCode, randomIP)
	return resp, targetModel, nil
}

type UpstreamData struct {
	Type string `json:"type"`
	Data struct {
		DeltaContent string `json:"delta_content"`
		EditContent  string `json:"edit_content"`
		Phase        string `json:"phase"`
		Done         bool   `json:"done"`
		Error        *struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		} `json:"error,omitempty"`
	} `json:"data"`
}

// HasError 检查上游响应是否包含错误
func (u *UpstreamData) HasError() bool {
	return u.Data.Error != nil && u.Data.Error.Code != ""
}

// GetErrorMessage 获取错误信息
func (u *UpstreamData) GetErrorMessage() string {
	if u.Data.Error == nil {
		return ""
	}
	if u.Data.Error.Detail != "" {
		return u.Data.Error.Detail
	}
	return u.Data.Error.Code
}

// UpstreamResult 上游请求结果
type UpstreamResult struct {
	Success      bool
	HasContent   bool
	ErrorMessage string
	OutputTokens int64
}

// 重试配置
const (
	MaxRetries   = 2 // 最大重试次数
	RetryableErr = "INTERNAL_ERROR"
)

func (u *UpstreamData) GetEditContent() string {
	editContent := u.Data.EditContent
	if editContent == "" {
		return ""
	}

	if len(editContent) > 0 && editContent[0] == '"' {
		var unescaped string
		if err := json.Unmarshal([]byte(editContent), &unescaped); err == nil {
			LogDebug("[GetEditContent] Unescaped edit_content from JSON string")
			return unescaped
		}
	}

	return editContent
}

type ThinkingFilter struct {
	hasSeenFirstThinking bool
	buffer               string
	lastOutputChunk      string
	lastPhase            string
	thinkingRoundCount   int
}

func (f *ThinkingFilter) ProcessThinking(deltaContent string) string {
	if !f.hasSeenFirstThinking {
		f.hasSeenFirstThinking = true
		if idx := strings.Index(deltaContent, "> "); idx != -1 {
			deltaContent = deltaContent[idx+2:]
		} else {
			return ""
		}
	}

	content := f.buffer + deltaContent
	f.buffer = ""

	content = strings.ReplaceAll(content, "\n> ", "\n")

	if strings.HasSuffix(content, "\n>") {
		f.buffer = "\n>"
		return content[:len(content)-2]
	}
	if strings.HasSuffix(content, "\n") {
		f.buffer = "\n"
		return content[:len(content)-1]
	}

	return content
}

func (f *ThinkingFilter) Flush() string {
	result := f.buffer
	f.buffer = ""
	return result
}

func (f *ThinkingFilter) ExtractCompleteThinking(editContent string) string {
	startIdx := strings.Index(editContent, "> ")
	if startIdx == -1 {
		return ""
	}
	startIdx += 2

	endIdx := strings.Index(editContent, "\n</details>")
	if endIdx == -1 {
		return ""
	}

	content := editContent[startIdx:endIdx]
	content = strings.ReplaceAll(content, "\n> ", "\n")
	return content
}

func (f *ThinkingFilter) ExtractIncrementalThinking(editContent string) string {
	completeThinking := f.ExtractCompleteThinking(editContent)
	if completeThinking == "" {
		return ""
	}

	if f.lastOutputChunk == "" {
		return completeThinking
	}

	idx := strings.Index(completeThinking, f.lastOutputChunk)
	if idx == -1 {
		return completeThinking
	}

	incrementalPart := completeThinking[idx+len(f.lastOutputChunk):]
	return incrementalPart
}

func (f *ThinkingFilter) ResetForNewRound() {
	f.lastOutputChunk = ""
	f.hasSeenFirstThinking = false
}

func HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// 只接受 POST 请求
	if r.Method != http.MethodPost {
		writeInvalidRequestError(w, "Only POST method is allowed")
		return
	}

	apiKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")

	// API Key 认证
	if !Cfg.SkipAuthToken {
		if apiKey == "" {
			LogDebug("Missing Authorization header")
			writeError(w, http.StatusUnauthorized, ErrTypeAuthentication, "Missing or invalid Authorization header", "invalid_api_key")
			return
		}
		// 验证 API Key
		if !ValidateAuthToken(apiKey) {
			LogDebug("Invalid API key: %s...", apiKey[:min(8, len(apiKey))])
			writeError(w, http.StatusUnauthorized, ErrTypeAuthentication, "Invalid API key", "invalid_api_key")
			return
		}
		LogDebug("API key validated: %s...", apiKey[:min(8, len(apiKey))])
	} else {
		LogDebug("SKIP_AUTH_TOKEN enabled, skipping API key validation")
	}
	clientIP := GetClientIP(r)
	isMultimodal := false

	var token string
	// 优先使用 TokenManager 中的 token
	if tmToken := GetTokenManager().GetToken(); tmToken != "" {
		token = tmToken
		LogDebug("Using token from TokenManager")
	} else if backupToken := GetBackupToken(); backupToken != "" {
		token = backupToken
		LogDebug("Using backup token")
	} else {
		anonymousToken, err := GetAnonymousToken()
		if err != nil {
			LogError("Failed to get anonymous token: %v", err)
			GetTokenManager().RecordCall(false, false)
			writeErrorResponse(w, http.StatusInternalServerError)
			return
		}
		token = anonymousToken
		LogDebug("Using anonymous token: %s...", token[:min(10, len(token))])
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInvalidRequestError(w, "无效的请求格式")
		return
	}

	if req.Model == "" {
		req.Model = "GLM-4.6"
	}

	// 验证模型是否存在
	if !IsValidModel(req.Model) {
		writeModelNotFoundError(w, req.Model)
		return
	}
	// 检测多模态
	reqImageURLs, reqVideoURLs := extractAllMediaURLs(req.Messages)
	if len(reqImageURLs) > 0 || len(reqVideoURLs) > 0 {
		isMultimodal = true
		LogDebug("[Request] Multimodal detected: images=%d, videos=%d", len(reqImageURLs), len(reqVideoURLs))
		for i, url := range reqImageURLs {
			urlPreview := url
			if len(urlPreview) > 80 {
				urlPreview = urlPreview[:80] + "..."
			}
			LogDebug("[Request] Image %d: %s", i+1, urlPreview)
		}
		for i, url := range reqVideoURLs {
			urlPreview := url
			if len(urlPreview) > 80 {
				urlPreview = urlPreview[:80] + "..."
			}
			LogDebug("[Request] Video %d: %s", i+1, urlPreview)
		}
	}

	// 处理工具调用
	messages := req.Messages
	if len(req.Tools) > 0 {
		messages = ProcessMessagesWithTools(messages, req.Tools, req.ToolChoice)
	}

	inputTokens := CountRequestTokens(messages, req.Tools)
	LogDebug("Chat request: model=%s, messages=%d, stream=%v, input_tokens=%d, ip=%s, multimodal=%v, tools=%d",
		req.Model, len(messages), req.Stream, inputTokens, clientIP, isMultimodal, len(req.Tools))

	completionID := fmt.Sprintf("chatcmpl-%s", uuid.New().String()[:29])
	includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage

	var outputTokens int64
	var lastError string
	success := false

	// 重试循环
	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			// 重试时获取新 token
			if newToken := GetTokenManager().GetToken(); newToken != "" && newToken != token {
				token = newToken
				LogInfo("Retry %d/%d with new token", attempt, MaxRetries)
			} else {
				LogInfo("Retry %d/%d with same token", attempt, MaxRetries)
			}
		}

		resp, modelName, err := makeUpstreamRequest(token, messages, req.Model, reqImageURLs, reqVideoURLs)
		if err != nil {
			LogError("Upstream request failed (attempt %d): %v", attempt+1, err)
			lastError = err.Error()
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			LogError("Upstream error (attempt %d): status=%d, body=%s", attempt+1, resp.StatusCode, string(body)[:min(500, len(body))])
			lastError = fmt.Sprintf("status %d", resp.StatusCode)
			// 非 5xx 错误不重试
			if resp.StatusCode < 500 {
				GetTokenManager().RecordCall(false, isMultimodal)
				writeUpstreamError(w, resp.StatusCode, body)
				return
			}
			continue
		}

		var result UpstreamResult
		if req.Stream {
			result = handleStreamResponseWithRetry(w, resp.Body, completionID, modelName, inputTokens, includeUsage, req.Tools, attempt == 0)
		} else {
			result = handleNonStreamResponseWithRetry(w, resp.Body, completionID, modelName, inputTokens, req.Tools)
		}
		resp.Body.Close()

		outputTokens = result.OutputTokens

		if result.Success && result.HasContent {
			success = true
			break
		}

		// 检查是否需要重试
		if result.ErrorMessage != "" {
			lastError = result.ErrorMessage
			LogWarn("Upstream returned error (attempt %d): %s", attempt+1, result.ErrorMessage)
		} else if !result.HasContent {
			lastError = "empty response"
			LogWarn("Upstream returned empty content (attempt %d)", attempt+1)
		}

		// 流式请求已开始写入，无法重试
		if req.Stream && attempt == 0 {
			LogDebug("Stream response already started, cannot retry")
			break
		}
	}

	if !success && !req.Stream {
		// 非流式请求失败，返回错误
		GetTokenManager().RecordCall(false, isMultimodal)
		writeError(w, http.StatusBadGateway, ErrTypeUpstream, fmt.Sprintf("请求失败: %s", lastError), "upstream_error")
		return
	}

	// 记录遥测数据
	RecordRequest(inputTokens, outputTokens, req.Model)
	GetTokenManager().RecordCall(success, isMultimodal)
	LogDebug("Chat completed: model=%s, input_tokens=%d, output_tokens=%d, ip=%s, success=%v",
		req.Model, inputTokens, outputTokens, clientIP, success)
}

func handleStreamResponse(w http.ResponseWriter, body io.ReadCloser, completionID, modelName string, inputTokens int64, includeUsage bool, tools []Tool) int64 {
	var outputTokens int64
	var fullContent strings.Builder
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return 0
	}

	// 发送第一个 chunk 带 role
	firstChunk := ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []Choice{{
			Index:        0,
			Delta:        &Delta{Role: "assistant"},
			FinishReason: nil,
		}},
	}
	data, _ := json.Marshal(firstChunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	hasContent := false
	searchRefFilter := NewSearchRefFilter()
	thinkingFilter := &ThinkingFilter{}
	pendingSourcesMarkdown := ""
	pendingImageSearchMarkdown := ""
	totalContentOutputLength := 0 // 记录已输出的 content 字符长度

	for scanner.Scan() {
		line := scanner.Text()
		LogDebug("[Upstream] %s", line)

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var upstream UpstreamData
		if err := json.Unmarshal([]byte(payload), &upstream); err != nil {
			continue
		}

		// 检测上游错误
		if upstream.HasError() {
			LogError("Upstream error: %s", upstream.GetErrorMessage())
			errContent := fmt.Sprintf("[上游服务错误: %s]", upstream.GetErrorMessage())
			errChunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{Content: errContent},
					FinishReason: nil,
				}},
			}
			errData, _ := json.Marshal(errChunk)
			fmt.Fprintf(w, "data: %s\n\n", errData)
			flusher.Flush()
			hasContent = true
			break
		}

		if upstream.Data.Phase == "done" {
			break
		}

		if upstream.Data.Phase == "thinking" && upstream.Data.DeltaContent != "" {
			isNewThinkingRound := false
			if thinkingFilter.lastPhase != "" && thinkingFilter.lastPhase != "thinking" {
				thinkingFilter.ResetForNewRound()
				thinkingFilter.thinkingRoundCount++
				isNewThinkingRound = true
			}
			thinkingFilter.lastPhase = "thinking"

			reasoningContent := thinkingFilter.ProcessThinking(upstream.Data.DeltaContent)

			if isNewThinkingRound && thinkingFilter.thinkingRoundCount > 1 && reasoningContent != "" {
				reasoningContent = "\n\n" + reasoningContent
			}

			if reasoningContent != "" {
				thinkingFilter.lastOutputChunk = reasoningContent
				reasoningContent = searchRefFilter.Process(reasoningContent)

				if reasoningContent != "" {
					hasContent = true
					chunk := ChatCompletionChunk{
						ID:      completionID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   modelName,
						Choices: []Choice{{
							Index:        0,
							Delta:        &Delta{ReasoningContent: reasoningContent},
							FinishReason: nil,
						}},
					}
					data, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
				}
			}
			continue
		}

		if upstream.Data.Phase != "" {
			thinkingFilter.lastPhase = upstream.Data.Phase
		}

		editContent := upstream.GetEditContent()
		if editContent != "" && IsSearchResultContent(editContent) {
			if results := ParseSearchResults(editContent); len(results) > 0 {
				searchRefFilter.AddSearchResults(results)
				pendingSourcesMarkdown = searchRefFilter.GetSearchResultsMarkdown()
			}
			continue
		}
		if editContent != "" && strings.Contains(editContent, `"search_image"`) {
			textBeforeBlock := ExtractTextBeforeGlmBlock(editContent)
			if textBeforeBlock != "" {
				textBeforeBlock = searchRefFilter.Process(textBeforeBlock)
				if textBeforeBlock != "" {
					hasContent = true
					chunk := ChatCompletionChunk{
						ID:      completionID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   modelName,
						Choices: []Choice{{
							Index:        0,
							Delta:        &Delta{Content: textBeforeBlock},
							FinishReason: nil,
						}},
					}
					data, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
				}
			}
			if results := ParseImageSearchResults(editContent); len(results) > 0 {
				pendingImageSearchMarkdown = FormatImageSearchResults(results)
			}
			continue
		}
		if editContent != "" && strings.Contains(editContent, `"mcp"`) {
			textBeforeBlock := ExtractTextBeforeGlmBlock(editContent)
			if textBeforeBlock != "" {
				textBeforeBlock = searchRefFilter.Process(textBeforeBlock)
				if textBeforeBlock != "" {
					hasContent = true
					chunk := ChatCompletionChunk{
						ID:      completionID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   modelName,
						Choices: []Choice{{
							Index:        0,
							Delta:        &Delta{Content: textBeforeBlock},
							FinishReason: nil,
						}},
					}
					data, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
				}
			}
			continue
		}
		if editContent != "" && IsSearchToolCall(editContent, upstream.Data.Phase) {
			continue
		}

		if pendingSourcesMarkdown != "" {
			hasContent = true
			chunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{Content: pendingSourcesMarkdown},
					FinishReason: nil,
				}},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			pendingSourcesMarkdown = ""
		}
		if pendingImageSearchMarkdown != "" {
			hasContent = true
			chunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{Content: pendingImageSearchMarkdown},
					FinishReason: nil,
				}},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			pendingImageSearchMarkdown = ""
		}

		content := ""
		reasoningContent := ""

		if thinkingRemaining := thinkingFilter.Flush(); thinkingRemaining != "" {
			thinkingFilter.lastOutputChunk = thinkingRemaining
			processedRemaining := searchRefFilter.Process(thinkingRemaining)
			if processedRemaining != "" {
				hasContent = true
				chunk := ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   modelName,
					Choices: []Choice{{
						Index:        0,
						Delta:        &Delta{ReasoningContent: processedRemaining},
						FinishReason: nil,
					}},
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}

		if pendingSourcesMarkdown != "" && thinkingFilter.hasSeenFirstThinking {
			hasContent = true
			chunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{ReasoningContent: pendingSourcesMarkdown},
					FinishReason: nil,
				}},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			pendingSourcesMarkdown = ""
		}

		if upstream.Data.Phase == "answer" && upstream.Data.DeltaContent != "" {
			content = upstream.Data.DeltaContent
		} else if upstream.Data.Phase == "answer" && editContent != "" {
			if strings.Contains(editContent, "</details>") {
				reasoningContent = thinkingFilter.ExtractIncrementalThinking(editContent)

				if idx := strings.Index(editContent, "</details>"); idx != -1 {
					afterDetails := editContent[idx+len("</details>"):]
					if strings.HasPrefix(afterDetails, "\n") {
						content = afterDetails[1:]
					} else {
						content = afterDetails
					}
					totalContentOutputLength = len([]rune(content))
				}
			}
		} else if (upstream.Data.Phase == "other" || upstream.Data.Phase == "tool_call") && editContent != "" {
			fullContent := editContent
			fullContentRunes := []rune(fullContent)

			if len(fullContentRunes) > totalContentOutputLength {
				content = string(fullContentRunes[totalContentOutputLength:])
				totalContentOutputLength = len(fullContentRunes)
			} else {
				content = fullContent
			}
		}

		if reasoningContent != "" {
			reasoningContent = searchRefFilter.Process(reasoningContent) + searchRefFilter.Flush()
		}
		if reasoningContent != "" {
			hasContent = true
			chunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{ReasoningContent: reasoningContent},
					FinishReason: nil,
				}},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}

		if content == "" {
			continue
		}

		content = searchRefFilter.Process(content)
		if content == "" {
			continue
		}

		hasContent = true
		if upstream.Data.Phase == "answer" && upstream.Data.DeltaContent != "" {
			totalContentOutputLength += len([]rune(content))
		}
		fullContent.WriteString(content)

		chunk := ChatCompletionChunk{
			ID:      completionID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelName,
			Choices: []Choice{{
				Index:        0,
				Delta:        &Delta{Content: content},
				FinishReason: nil,
			}},
		}

		outputTokens += CountTokens(content)
		chunkData, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", chunkData)
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		LogError("[Upstream] scanner error: %v", err)
	}

	if remaining := searchRefFilter.Flush(); remaining != "" {
		hasContent = true
		fullContent.WriteString(remaining)
		chunk := ChatCompletionChunk{
			ID:      completionID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelName,
			Choices: []Choice{{
				Index:        0,
				Delta:        &Delta{Content: remaining},
				FinishReason: nil,
			}},
		}
		chunkData, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", chunkData)
		flusher.Flush()
	}

	if !hasContent {
		LogError("Stream response 200 but no content received")
	}
	stopReason := "stop"
	var toolCalls []ToolCall
	if len(tools) > 0 {
		rawContent := fullContent.String()
		toolCalls = ExtractToolInvocations(rawContent)
		if len(toolCalls) > 0 {
			stopReason = "tool_calls"
			LogDebug("[Stream] Detected %d tool calls, sending tool_calls chunks", len(toolCalls))
			for i, tc := range toolCalls {
				if tc.ID == "" {
					tc.ID = generateCallID()
				}
				toolChunk := ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   modelName,
					Choices: []Choice{{
						Index: 0,
						Delta: &Delta{
							ToolCalls: []ToolCall{{
								Index:    i,
								ID:       tc.ID,
								Type:     tc.Type,
								Function: tc.Function,
							}},
						},
						FinishReason: nil,
					}},
				}
				toolData, _ := json.Marshal(toolChunk)
				fmt.Fprintf(w, "data: %s\n\n", toolData)
				flusher.Flush()
			}
		}
	}

	finalChunk := ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []Choice{{
			Index:        0,
			Delta:        &Delta{},
			FinishReason: &stopReason,
		}},
	}

	finalData, _ := json.Marshal(finalChunk)
	fmt.Fprintf(w, "data: %s\n\n", finalData)
	if includeUsage {
		usageChunk := ChatCompletionChunkResponse{
			ID:      completionID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelName,
			Choices: []Choice{},
			Usage: &Usage{
				PromptTokens:     inputTokens,
				CompletionTokens: outputTokens,
				TotalTokens:      inputTokens + outputTokens,
			},
		}
		usageData, _ := json.Marshal(usageChunk)
		fmt.Fprintf(w, "data: %s\n\n", usageData)
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	return outputTokens
}

func handleNonStreamResponse(w http.ResponseWriter, body io.ReadCloser, completionID, modelName string, inputTokens int64, tools []Tool) int64 {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-Id", completionID)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	var outputTokens int64
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var chunks []string
	var reasoningChunks []string
	thinkingFilter := &ThinkingFilter{}
	searchRefFilter := NewSearchRefFilter()
	hasThinking := false
	pendingSourcesMarkdown := ""
	pendingImageSearchMarkdown := ""

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var upstream UpstreamData
		if err := json.Unmarshal([]byte(payload), &upstream); err != nil {
			continue
		}
		if upstream.HasError() {
			LogError("Upstream error: %s", upstream.GetErrorMessage())
			chunks = append(chunks, fmt.Sprintf("[上游服务错误: %s]", upstream.GetErrorMessage()))
			break
		}

		if upstream.Data.Phase == "done" {
			break
		}

		if upstream.Data.Phase == "thinking" && upstream.Data.DeltaContent != "" {
			if thinkingFilter.lastPhase != "" && thinkingFilter.lastPhase != "thinking" {
				thinkingFilter.ResetForNewRound()
				thinkingFilter.thinkingRoundCount++
				if thinkingFilter.thinkingRoundCount > 1 {
					reasoningChunks = append(reasoningChunks, "\n\n")
				}
			}
			thinkingFilter.lastPhase = "thinking"

			hasThinking = true
			reasoningContent := thinkingFilter.ProcessThinking(upstream.Data.DeltaContent)
			if reasoningContent != "" {
				thinkingFilter.lastOutputChunk = reasoningContent
				reasoningChunks = append(reasoningChunks, reasoningContent)
			}
			continue
		}

		if upstream.Data.Phase != "" {
			thinkingFilter.lastPhase = upstream.Data.Phase
		}

		editContent := upstream.GetEditContent()
		if editContent != "" && IsSearchResultContent(editContent) {
			if results := ParseSearchResults(editContent); len(results) > 0 {
				searchRefFilter.AddSearchResults(results)
				pendingSourcesMarkdown = searchRefFilter.GetSearchResultsMarkdown()
			}
			continue
		}
		if editContent != "" && strings.Contains(editContent, `"search_image"`) {
			textBeforeBlock := ExtractTextBeforeGlmBlock(editContent)
			if textBeforeBlock != "" {
				chunks = append(chunks, textBeforeBlock)
			}
			if results := ParseImageSearchResults(editContent); len(results) > 0 {
				pendingImageSearchMarkdown = FormatImageSearchResults(results)
			}
			continue
		}
		if editContent != "" && strings.Contains(editContent, `"mcp"`) {
			textBeforeBlock := ExtractTextBeforeGlmBlock(editContent)
			if textBeforeBlock != "" {
				chunks = append(chunks, textBeforeBlock)
			}
			continue
		}
		if editContent != "" && IsSearchToolCall(editContent, upstream.Data.Phase) {
			continue
		}

		if pendingSourcesMarkdown != "" {
			if hasThinking {
				reasoningChunks = append(reasoningChunks, pendingSourcesMarkdown)
			} else {
				chunks = append(chunks, pendingSourcesMarkdown)
			}
			pendingSourcesMarkdown = ""
		}
		if pendingImageSearchMarkdown != "" {
			chunks = append(chunks, pendingImageSearchMarkdown)
			pendingImageSearchMarkdown = ""
		}

		content := ""
		if upstream.Data.Phase == "answer" && upstream.Data.DeltaContent != "" {
			content = upstream.Data.DeltaContent
		} else if upstream.Data.Phase == "answer" && editContent != "" {
			if strings.Contains(editContent, "</details>") {
				reasoningContent := thinkingFilter.ExtractIncrementalThinking(editContent)
				if reasoningContent != "" {
					reasoningChunks = append(reasoningChunks, reasoningContent)
				}

				if idx := strings.Index(editContent, "</details>"); idx != -1 {
					afterDetails := editContent[idx+len("</details>"):]
					if strings.HasPrefix(afterDetails, "\n") {
						content = afterDetails[1:]
					} else {
						content = afterDetails
					}
				}
			}
		} else if (upstream.Data.Phase == "other" || upstream.Data.Phase == "tool_call") && editContent != "" {
			content = editContent
		}

		if content != "" {
			chunks = append(chunks, content)
		}
	}

	fullContent := strings.Join(chunks, "")
	fullContent = searchRefFilter.Process(fullContent) + searchRefFilter.Flush()
	fullReasoning := strings.Join(reasoningChunks, "")
	fullReasoning = searchRefFilter.Process(fullReasoning) + searchRefFilter.Flush()

	if fullContent == "" && fullReasoning == "" {
		LogError("Non-stream response 200 but no content received")
	}
	stopReason := "stop"
	var toolCalls []ToolCall
	if len(tools) > 0 {
		toolCalls = ExtractToolInvocations(fullContent)
		if len(toolCalls) > 0 {
			stopReason = "tool_calls"
			fullContent = RemoveToolJSONContent(fullContent)
		}
	}
	outputTokens = CountTokens(fullContent) + CountTokens(fullReasoning)

	response := ChatCompletionResponse{
		ID:      completionID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []Choice{{
			Index: 0,
			Message: &MessageResp{
				Role:             "assistant",
				Content:          fullContent,
				ReasoningContent: fullReasoning,
				ToolCalls:        toolCalls,
			},
			FinishReason: &stopReason,
		}},
		Usage: &Usage{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      inputTokens + outputTokens,
		},
		SystemFingerprint: "openai",
	}
	json.NewEncoder(w).Encode(response)
	return outputTokens
}
func handleStreamResponseWithRetry(w http.ResponseWriter, body io.ReadCloser, completionID, modelName string, inputTokens int64, includeUsage bool, tools []Tool, isFirstAttempt bool) UpstreamResult {
	result := UpstreamResult{Success: true, HasContent: false}
	var outputTokens int64
	var fullContent strings.Builder
	var upstreamError string
	if isFirstAttempt {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		result.Success = false
		result.ErrorMessage = "streaming not supported"
		return result
	}
	firstChunk := ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []Choice{{
			Index:        0,
			Delta:        &Delta{Role: "assistant"},
			FinishReason: nil,
		}},
	}
	data, _ := json.Marshal(firstChunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	hasContent := false
	searchRefFilter := NewSearchRefFilter()
	thinkingFilter := &ThinkingFilter{}
	pendingSourcesMarkdown := ""
	pendingImageSearchMarkdown := ""
	totalContentOutputLength := 0

	for scanner.Scan() {
		line := scanner.Text()
		LogDebug("[Upstream] %s", line)

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var upstream UpstreamData
		if err := json.Unmarshal([]byte(payload), &upstream); err != nil {
			continue
		}
		if upstream.HasError() {
			upstreamError = upstream.GetErrorMessage()
			LogError("Upstream error: %s", upstreamError)
			errContent := fmt.Sprintf("[上游服务错误: %s]", upstreamError)
			errChunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{Content: errContent},
					FinishReason: nil,
				}},
			}
			errData, _ := json.Marshal(errChunk)
			fmt.Fprintf(w, "data: %s\n\n", errData)
			flusher.Flush()
			hasContent = true
			result.Success = false
			result.ErrorMessage = upstreamError
			break
		}

		if upstream.Data.Phase == "done" {
			break
		}

		if upstream.Data.Phase == "thinking" && upstream.Data.DeltaContent != "" {
			isNewThinkingRound := false
			if thinkingFilter.lastPhase != "" && thinkingFilter.lastPhase != "thinking" {
				thinkingFilter.ResetForNewRound()
				thinkingFilter.thinkingRoundCount++
				isNewThinkingRound = true
			}
			thinkingFilter.lastPhase = "thinking"

			reasoningContent := thinkingFilter.ProcessThinking(upstream.Data.DeltaContent)

			if isNewThinkingRound && thinkingFilter.thinkingRoundCount > 1 && reasoningContent != "" {
				reasoningContent = "\n\n" + reasoningContent
			}

			if reasoningContent != "" {
				thinkingFilter.lastOutputChunk = reasoningContent
				reasoningContent = searchRefFilter.Process(reasoningContent)

				if reasoningContent != "" {
					hasContent = true
					chunk := ChatCompletionChunk{
						ID:      completionID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   modelName,
						Choices: []Choice{{
							Index:        0,
							Delta:        &Delta{ReasoningContent: reasoningContent},
							FinishReason: nil,
						}},
					}
					chunkData, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", chunkData)
					flusher.Flush()
				}
			}
			continue
		}

		if upstream.Data.Phase != "" {
			thinkingFilter.lastPhase = upstream.Data.Phase
		}

		editContent := upstream.GetEditContent()
		if editContent != "" && IsSearchResultContent(editContent) {
			if results := ParseSearchResults(editContent); len(results) > 0 {
				searchRefFilter.AddSearchResults(results)
				pendingSourcesMarkdown = searchRefFilter.GetSearchResultsMarkdown()
			}
			continue
		}
		if editContent != "" && strings.Contains(editContent, `"search_image"`) {
			textBeforeBlock := ExtractTextBeforeGlmBlock(editContent)
			if textBeforeBlock != "" {
				textBeforeBlock = searchRefFilter.Process(textBeforeBlock)
				if textBeforeBlock != "" {
					hasContent = true
					chunk := ChatCompletionChunk{
						ID:      completionID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   modelName,
						Choices: []Choice{{
							Index:        0,
							Delta:        &Delta{Content: textBeforeBlock},
							FinishReason: nil,
						}},
					}
					chunkData, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", chunkData)
					flusher.Flush()
				}
			}
			if results := ParseImageSearchResults(editContent); len(results) > 0 {
				pendingImageSearchMarkdown = FormatImageSearchResults(results)
			}
			continue
		}
		if editContent != "" && strings.Contains(editContent, `"mcp"`) {
			textBeforeBlock := ExtractTextBeforeGlmBlock(editContent)
			if textBeforeBlock != "" {
				textBeforeBlock = searchRefFilter.Process(textBeforeBlock)
				if textBeforeBlock != "" {
					hasContent = true
					chunk := ChatCompletionChunk{
						ID:      completionID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   modelName,
						Choices: []Choice{{
							Index:        0,
							Delta:        &Delta{Content: textBeforeBlock},
							FinishReason: nil,
						}},
					}
					chunkData, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", chunkData)
					flusher.Flush()
				}
			}
			continue
		}
		if editContent != "" && IsSearchToolCall(editContent, upstream.Data.Phase) {
			continue
		}

		if pendingSourcesMarkdown != "" {
			hasContent = true
			chunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{Content: pendingSourcesMarkdown},
					FinishReason: nil,
				}},
			}
			chunkData, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", chunkData)
			flusher.Flush()
			pendingSourcesMarkdown = ""
		}
		if pendingImageSearchMarkdown != "" {
			hasContent = true
			chunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{Content: pendingImageSearchMarkdown},
					FinishReason: nil,
				}},
			}
			chunkData, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", chunkData)
			flusher.Flush()
			pendingImageSearchMarkdown = ""
		}

		content := ""
		reasoningContent := ""

		if thinkingRemaining := thinkingFilter.Flush(); thinkingRemaining != "" {
			thinkingFilter.lastOutputChunk = thinkingRemaining
			processedRemaining := searchRefFilter.Process(thinkingRemaining)
			if processedRemaining != "" {
				hasContent = true
				chunk := ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   modelName,
					Choices: []Choice{{
						Index:        0,
						Delta:        &Delta{ReasoningContent: processedRemaining},
						FinishReason: nil,
					}},
				}
				chunkData, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", chunkData)
				flusher.Flush()
			}
		}

		if pendingSourcesMarkdown != "" && thinkingFilter.hasSeenFirstThinking {
			hasContent = true
			chunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{ReasoningContent: pendingSourcesMarkdown},
					FinishReason: nil,
				}},
			}
			chunkData, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", chunkData)
			flusher.Flush()
			pendingSourcesMarkdown = ""
		}

		if upstream.Data.Phase == "answer" && upstream.Data.DeltaContent != "" {
			content = upstream.Data.DeltaContent
		} else if upstream.Data.Phase == "answer" && editContent != "" {
			if strings.Contains(editContent, "</details>") {
				reasoningContent = thinkingFilter.ExtractIncrementalThinking(editContent)

				if idx := strings.Index(editContent, "</details>"); idx != -1 {
					afterDetails := editContent[idx+len("</details>"):]
					if strings.HasPrefix(afterDetails, "\n") {
						content = afterDetails[1:]
					} else {
						content = afterDetails
					}
					totalContentOutputLength = len([]rune(content))
				}
			}
		} else if (upstream.Data.Phase == "other" || upstream.Data.Phase == "tool_call") && editContent != "" {
			fullEditContent := editContent
			fullContentRunes := []rune(fullEditContent)

			if len(fullContentRunes) > totalContentOutputLength {
				content = string(fullContentRunes[totalContentOutputLength:])
				totalContentOutputLength = len(fullContentRunes)
			} else {
				content = fullEditContent
			}
		}

		if reasoningContent != "" {
			reasoningContent = searchRefFilter.Process(reasoningContent) + searchRefFilter.Flush()
		}
		if reasoningContent != "" {
			hasContent = true
			chunk := ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []Choice{{
					Index:        0,
					Delta:        &Delta{ReasoningContent: reasoningContent},
					FinishReason: nil,
				}},
			}
			chunkData, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", chunkData)
			flusher.Flush()
		}

		if content == "" {
			continue
		}

		content = searchRefFilter.Process(content)
		if content == "" {
			continue
		}

		hasContent = true
		if upstream.Data.Phase == "answer" && upstream.Data.DeltaContent != "" {
			totalContentOutputLength += len([]rune(content))
		}
		fullContent.WriteString(content)

		chunk := ChatCompletionChunk{
			ID:      completionID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelName,
			Choices: []Choice{{
				Index:        0,
				Delta:        &Delta{Content: content},
				FinishReason: nil,
			}},
		}

		outputTokens += CountTokens(content)
		chunkData, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", chunkData)
		flusher.Flush()
	}

	if remaining := searchRefFilter.Flush(); remaining != "" {
		hasContent = true
		fullContent.WriteString(remaining)
		chunk := ChatCompletionChunk{
			ID:      completionID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelName,
			Choices: []Choice{{
				Index:        0,
				Delta:        &Delta{Content: remaining},
				FinishReason: nil,
			}},
		}
		chunkData, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", chunkData)
		flusher.Flush()
	}

	// 检测工具调用
	stopReason := "stop"
	var toolCalls []ToolCall
	if len(tools) > 0 {
		toolCalls = ExtractToolInvocations(fullContent.String())
		if len(toolCalls) > 0 {
			stopReason = "tool_calls"
			for i, tc := range toolCalls {
				toolChunk := ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   modelName,
					Choices: []Choice{{
						Index: 0,
						Delta: &Delta{
							ToolCalls: []ToolCall{{
								Index:    i,
								ID:       tc.ID,
								Type:     tc.Type,
								Function: tc.Function,
							}},
						},
						FinishReason: nil,
					}},
				}
				toolData, _ := json.Marshal(toolChunk)
				fmt.Fprintf(w, "data: %s\n\n", toolData)
				flusher.Flush()
			}
		}
	}

	finalChunk := ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []Choice{{
			Index:        0,
			Delta:        &Delta{},
			FinishReason: &stopReason,
		}},
	}
	finalData, _ := json.Marshal(finalChunk)
	fmt.Fprintf(w, "data: %s\n\n", finalData)

	if includeUsage {
		usageChunk := ChatCompletionChunkResponse{
			ID:      completionID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelName,
			Choices: []Choice{},
			Usage: &Usage{
				PromptTokens:     inputTokens,
				CompletionTokens: outputTokens,
				TotalTokens:      inputTokens + outputTokens,
			},
		}
		usageData, _ := json.Marshal(usageChunk)
		fmt.Fprintf(w, "data: %s\n\n", usageData)
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	result.HasContent = hasContent
	result.OutputTokens = outputTokens
	if !hasContent && result.ErrorMessage == "" {
		result.ErrorMessage = "empty response"
	}
	return result
}

// handleNonStreamResponseWithRetry 非流式响应处理（带重试支持，不立即写入响应）
func handleNonStreamResponseWithRetry(w http.ResponseWriter, body io.ReadCloser, completionID, modelName string, inputTokens int64, tools []Tool) UpstreamResult {
	result := UpstreamResult{Success: true, HasContent: false}
	var outputTokens int64
	var upstreamError string

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var chunks []string
	var reasoningChunks []string
	thinkingFilter := &ThinkingFilter{}
	searchRefFilter := NewSearchRefFilter()
	hasThinking := false
	pendingSourcesMarkdown := ""
	pendingImageSearchMarkdown := ""

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var upstream UpstreamData
		if err := json.Unmarshal([]byte(payload), &upstream); err != nil {
			continue
		}

		// 检测上游错误
		if upstream.HasError() {
			upstreamError = upstream.GetErrorMessage()
			LogError("Upstream error: %s", upstreamError)
			result.Success = false
			result.ErrorMessage = upstreamError
			return result
		}

		if upstream.Data.Phase == "done" {
			break
		}

		if upstream.Data.Phase == "thinking" && upstream.Data.DeltaContent != "" {
			if thinkingFilter.lastPhase != "" && thinkingFilter.lastPhase != "thinking" {
				thinkingFilter.ResetForNewRound()
				thinkingFilter.thinkingRoundCount++
				if thinkingFilter.thinkingRoundCount > 1 {
					reasoningChunks = append(reasoningChunks, "\n\n")
				}
			}
			thinkingFilter.lastPhase = "thinking"

			hasThinking = true
			reasoningContent := thinkingFilter.ProcessThinking(upstream.Data.DeltaContent)
			if reasoningContent != "" {
				thinkingFilter.lastOutputChunk = reasoningContent
				reasoningChunks = append(reasoningChunks, reasoningContent)
			}
			continue
		}

		if upstream.Data.Phase != "" {
			thinkingFilter.lastPhase = upstream.Data.Phase
		}

		editContent := upstream.GetEditContent()
		if editContent != "" && IsSearchResultContent(editContent) {
			if results := ParseSearchResults(editContent); len(results) > 0 {
				searchRefFilter.AddSearchResults(results)
				pendingSourcesMarkdown = searchRefFilter.GetSearchResultsMarkdown()
			}
			continue
		}
		if editContent != "" && strings.Contains(editContent, `"search_image"`) {
			textBeforeBlock := ExtractTextBeforeGlmBlock(editContent)
			if textBeforeBlock != "" {
				chunks = append(chunks, textBeforeBlock)
			}
			if results := ParseImageSearchResults(editContent); len(results) > 0 {
				pendingImageSearchMarkdown = FormatImageSearchResults(results)
			}
			continue
		}
		if editContent != "" && strings.Contains(editContent, `"mcp"`) {
			textBeforeBlock := ExtractTextBeforeGlmBlock(editContent)
			if textBeforeBlock != "" {
				chunks = append(chunks, textBeforeBlock)
			}
			continue
		}
		if editContent != "" && IsSearchToolCall(editContent, upstream.Data.Phase) {
			continue
		}

		if pendingSourcesMarkdown != "" {
			if hasThinking {
				reasoningChunks = append(reasoningChunks, pendingSourcesMarkdown)
			} else {
				chunks = append(chunks, pendingSourcesMarkdown)
			}
			pendingSourcesMarkdown = ""
		}
		if pendingImageSearchMarkdown != "" {
			chunks = append(chunks, pendingImageSearchMarkdown)
			pendingImageSearchMarkdown = ""
		}

		content := ""
		if upstream.Data.Phase == "answer" && upstream.Data.DeltaContent != "" {
			content = upstream.Data.DeltaContent
		} else if upstream.Data.Phase == "answer" && editContent != "" {
			if strings.Contains(editContent, "</details>") {
				reasoningContent := thinkingFilter.ExtractIncrementalThinking(editContent)
				if reasoningContent != "" {
					reasoningChunks = append(reasoningChunks, reasoningContent)
				}

				if idx := strings.Index(editContent, "</details>"); idx != -1 {
					afterDetails := editContent[idx+len("</details>"):]
					if strings.HasPrefix(afterDetails, "\n") {
						content = afterDetails[1:]
					} else {
						content = afterDetails
					}
				}
			}
		} else if (upstream.Data.Phase == "other" || upstream.Data.Phase == "tool_call") && editContent != "" {
			content = editContent
		}

		if content != "" {
			chunks = append(chunks, content)
		}
	}

	fullContent := strings.Join(chunks, "")
	fullContent = searchRefFilter.Process(fullContent) + searchRefFilter.Flush()
	fullReasoning := strings.Join(reasoningChunks, "")
	fullReasoning = searchRefFilter.Process(fullReasoning) + searchRefFilter.Flush()

	// 检查是否有内容
	if fullContent == "" && fullReasoning == "" {
		result.HasContent = false
		result.ErrorMessage = "empty response"
		return result
	}

	result.HasContent = true

	// 检测工具调用
	stopReason := "stop"
	var toolCalls []ToolCall
	if len(tools) > 0 {
		toolCalls = ExtractToolInvocations(fullContent)
		if len(toolCalls) > 0 {
			stopReason = "tool_calls"
			fullContent = RemoveToolJSONContent(fullContent)
		}
	}

	// 计算输出 token
	outputTokens = CountTokens(fullContent) + CountTokens(fullReasoning)
	result.OutputTokens = outputTokens

	// 写入响应
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-Id", completionID)

	response := ChatCompletionResponse{
		ID:      completionID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []Choice{{
			Index: 0,
			Message: &MessageResp{
				Role:             "assistant",
				Content:          fullContent,
				ReasoningContent: fullReasoning,
				ToolCalls:        toolCalls,
			},
			FinishReason: &stopReason,
		}},
		Usage: &Usage{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      inputTokens + outputTokens,
		},
		SystemFingerprint: "openai",
	}
	json.NewEncoder(w).Encode(response)
	return result
}

func HandleModels(w http.ResponseWriter, r *http.Request) {
	models := GetAvailableModels()

	response := ModelsResponse{
		Object: "list",
		Data:   models,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
