package internal

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

type ZAIModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OwnedBy string `json:"owned_by"`
	Created int64  `json:"created"`
}
type ZAIModelsResponse struct {
	Data []ZAIModel `json:"data"`
}
type ModelMapping struct {
	DisplayName       string
	UpstreamModelID   string
	UpstreamModelName string
	EnableThinking    bool
	WebSearch         bool
	AutoWebSearch     bool
	MCPServers        []string
	OwnedBy           string
	IsBuiltin         bool
}

var (
	modelMappings = make(map[string]ModelMapping)
	mappingsLock  sync.RWMutex
)

func registerBuiltinModel(id, upstreamID, upstreamName string, enableThinking, webSearch, autoWebSearch bool, mcpServers []string) {
	modelMappings[id] = ModelMapping{
		DisplayName:       id,
		UpstreamModelID:   upstreamID,
		UpstreamModelName: upstreamName,
		EnableThinking:    enableThinking,
		WebSearch:         webSearch,
		AutoWebSearch:     autoWebSearch,
		MCPServers:        mcpServers,
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
}

func defaultMCPServersForModel(modelID string) []string {
	servers := []string{"advanced-search"}
	if isVisionModelID(strings.ToLower(modelID)) {
		servers = append(servers, "vlm-image-search", "vlm-image-recognition", "vlm-image-processing")
	}
	return servers
}

func isVisionModelID(idLower string) bool {
	return strings.Contains(idLower, "-v") ||
		strings.Contains(idLower, "4.6v") ||
		strings.Contains(idLower, "5v") ||
		strings.Contains(idLower, ".1v")
}

func initBuiltinMappings() {
	mappingsLock.Lock()
	defer mappingsLock.Unlock()
	registerBuiltinModel("GLM-4.5", "0727-360B-API", "GLM-4.5", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.5-Thinking", "0727-360B-API", "GLM-4.5-Thinking", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.5-Search", "0727-360B-API", "GLM-4.5-Search", true, true, true, []string{"advanced-search", "deep-web-search"})
	registerBuiltinModel("GLM-4.5-Air", "0727-106B-API", "GLM-4.5-Air", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.6", "GLM-4-6-API-V1", "GLM-4.6", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.6-Thinking", "GLM-4-6-API-V1", "GLM-4.6-Thinking", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.6-Search", "GLM-4-6-API-V1", "GLM-4.6-Search", true, true, true, []string{"advanced-search", "deep-web-search"})
	registerBuiltinModel("GLM-4.7", "glm-4.7", "GLM-4.7", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.7-Thinking", "glm-4.7", "GLM-4.7-Thinking", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.7-Search", "glm-4.7", "GLM-4.7-Search", true, true, true, []string{"advanced-search", "deep-web-search"})
	registerBuiltinModel("GLM-4.5-V", "glm-4.5v", "GLM-4.5-V", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.6-V", "glm-4.6v", "GLM-4.6-V", true, false, true, []string{"advanced-search", "vlm-image-search", "vlm-image-recognition", "vlm-image-processing"})
	registerBuiltinModel("glm-4.6v", "glm-4.6v", "glm-4.6v", true, false, true, []string{"advanced-search", "vlm-image-search", "vlm-image-recognition", "vlm-image-processing"})
	registerBuiltinModel("GLM-5", "glm-5", "GLM-5", false, false, false, []string{})
	registerBuiltinModel("GLM-5-Thinking", "glm-5", "GLM-5-Thinking", true, false, false, []string{})
	registerBuiltinModel("GLM-5-Search", "glm-5", "GLM-5-Search", true, true, true, []string{"advanced-search", "deep-web-search"})
	registerBuiltinModel("GLM-5-Turbo", "GLM-5-Turbo", "GLM-5-Turbo", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-5v-Turbo", "GLM-5v-Turbo", "GLM-5v-Turbo", true, false, true, []string{"advanced-search", "vlm-image-search", "vlm-image-recognition", "vlm-image-processing"})
	registerBuiltinModel("GLM-5.1", "GLM-5.1", "GLM-5.1", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("glm-4-flash", "glm-4-flash", "glm-4-flash", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("glm-4-air-250414", "glm-4-air-250414", "glm-4-air-250414", true, false, true, []string{"advanced-search"})
	registerBuiltinModel("GLM-4.1V-Thinking-FlashX", "GLM-4.1V-Thinking-FlashX", "GLM-4.1V-Thinking-FlashX", true, false, true, []string{"advanced-search", "vlm-image-search", "vlm-image-recognition", "vlm-image-processing"})
}
func GetModelMapping(modelID string) (ModelMapping, bool) {
	baseModel, enableThinking, enableSearch := ParseModelName(modelID)

	mappingsLock.RLock()
	defer mappingsLock.RUnlock()

	if mapping, ok := modelMappings[baseModel]; ok {
		mapping.EnableThinking = enableThinking
		mapping.WebSearch = enableSearch
		mapping.AutoWebSearch = enableSearch
		return mapping, true
	}

	if mapping, ok := modelMappings[modelID]; ok {
		return mapping, true
	}

	return ModelMapping{}, false
}
func GetUpstreamConfig(requestedModel string) *ModelMapping {
	mapping, ok := GetModelMapping(requestedModel)
	if !ok {
		return nil
	}
	return &mapping
}

func fetchLatestModels() {
	token, err := GetUpstreamTokenForModelAPI()
	if err != nil {
		LogDebug("Failed to get token for model fetching: %v", err)
		return
	}
	req, err := fhttp.NewRequest("GET", "https://chat.z.ai/api/models", nil)
	if err != nil {
		LogDebug("Failed to create model request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	ApplyBrowserFingerprintHeaders(req.Header)
	client, err := TLSHTTPClient(10 * time.Second)
	if err != nil {
		LogDebug("Failed to create tls client: %v", err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		LogDebug("Failed to fetch models: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		LogDebug("Model API returned status %d", resp.StatusCode)
		return
	}
	var modelsResp ZAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		LogDebug("Failed to decode models response: %v", err)
		return
	}

	// 更新动态映射
	updateDynamicMappings(modelsResp.Data)

	LogInfo("Fetched %d models from API", len(modelsResp.Data))
}

// inferModelConfig 根据模型名称自动推断配置
func inferModelConfig(modelID string) (enableThinking bool, autoWebSearch bool, mcpServers []string) {
	idLower := strings.ToLower(modelID)
	enableThinking = true
	autoWebSearch = true
	mcpServers = defaultMCPServersForModel(idLower)
	return
}

// updateDynamicMappings 更新动态模型映射，将从 API 拉取的新模型注册到映射表
func updateDynamicMappings(models []ZAIModel) {
	mappingsLock.Lock()
	defer mappingsLock.Unlock()

	newCount := 0
	for _, model := range models {
		idLower := strings.ToLower(model.ID)
		if !strings.HasPrefix(idLower, "glm") {
			continue
		}
		if _, exists := modelMappings[model.ID]; exists {
			continue
		}
		alreadyMapped := false
		for existingID := range modelMappings {
			if strings.EqualFold(existingID, model.ID) {
				alreadyMapped = true
				break
			}
		}
		if alreadyMapped {
			continue
		}
		// 上游模型ID 格式化为显示名
		displayName := model.Name
		if displayName == "" {
			displayName = model.ID
		}
		ownedBy := model.OwnedBy
		if ownedBy == "" || ownedBy == "openai" {
			ownedBy = "z.ai"
		}
		enableThinking, autoWebSearch, mcpServers := inferModelConfig(model.ID)
		modelMappings[model.ID] = ModelMapping{
			DisplayName:       displayName,
			UpstreamModelID:   model.ID,
			UpstreamModelName: displayName,
			EnableThinking:    enableThinking,
			AutoWebSearch:     autoWebSearch,
			MCPServers:        mcpServers,
			OwnedBy:           ownedBy,
			IsBuiltin:         false,
		}
		newCount++
	}
	if newCount > 0 {
		LogInfo("Registered %d new dynamic models", newCount)
	}
}

// modelSuffixes 可用的后缀组合
var modelSuffixes = []string{
	"-thinking",        // 思考
	"-search",          // 搜索
	"-thinking-search", // 思考+搜索
}

// isBaseSuffixModel 判断模型是否为基础模型（不含 -Thinking/-Search 后缀）从而可以生成后缀组合
func isBaseSuffixModel(modelID string) bool {
	idLower := strings.ToLower(modelID)
	return !strings.HasSuffix(idLower, "-thinking") &&
		!strings.HasSuffix(idLower, "-search") &&
		!strings.HasSuffix(idLower, "-thinking-search")
}

// GetAvailableModels 获取所有可用模型，包括内置 + 动态 + 后缀组合
func GetAvailableModels() []ModelInfo {
	mappingsLock.RLock()
	defer mappingsLock.RUnlock()

	seen := make(map[string]bool)
	var models []ModelInfo

	addModel := func(id, ownedBy string) {
		key := strings.ToLower(id)
		if seen[key] {
			return
		}
		seen[key] = true
		models = append(models, ModelInfo{
			ID:      id,
			Object:  "model",
			OwnedBy: ownedBy,
		})
	}

	for id, m := range modelMappings {
		addModel(id, m.OwnedBy)
		if isBaseSuffixModel(id) {
			for _, suffix := range modelSuffixes {
				addModel(id+suffix, m.OwnedBy)
			}
		}
	}

	return models
}

// StartModelFetcher 启动模型获取定时器
func StartModelFetcher() {
	initBuiltinMappings()

	// 初次获取
	go fetchLatestModels()

	// 定期更新（每5分钟）
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for range ticker.C {
			fetchLatestModels()
		}
	}()
}
