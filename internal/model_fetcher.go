package internal

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
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

func initBuiltinMappings() {
	mappingsLock.Lock()
	defer mappingsLock.Unlock()
	modelMappings[Cfg.PrimaryModel] = ModelMapping{
		DisplayName:       Cfg.PrimaryModel,
		UpstreamModelID:   "0727-360B-API",
		UpstreamModelName: "GLM-4.5",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings[Cfg.ThinkingModel] = ModelMapping{
		DisplayName:       Cfg.ThinkingModel,
		UpstreamModelID:   "0727-360B-API",
		UpstreamModelName: "GLM-4.5-Thinking",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings[Cfg.SearchModel] = ModelMapping{
		DisplayName:       Cfg.SearchModel,
		UpstreamModelID:   "0727-360B-API",
		UpstreamModelName: "GLM-4.5-Search",
		EnableThinking:    true,
		WebSearch:         true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search", "deep-web-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings[Cfg.AirModel] = ModelMapping{
		DisplayName:       Cfg.AirModel,
		UpstreamModelID:   "0727-106B-API",
		UpstreamModelName: "GLM-4.5-Air",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings[Cfg.PrimaryModelNew] = ModelMapping{
		DisplayName:       Cfg.PrimaryModelNew,
		UpstreamModelID:   "GLM-4-6-API-V1",
		UpstreamModelName: "GLM-4.6",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings[Cfg.ThinkingModelNew] = ModelMapping{
		DisplayName:       Cfg.ThinkingModelNew,
		UpstreamModelID:   "GLM-4-6-API-V1",
		UpstreamModelName: "GLM-4.6-Thinking",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings[Cfg.SearchModelNew] = ModelMapping{
		DisplayName:       Cfg.SearchModelNew,
		UpstreamModelID:   "GLM-4-6-API-V1",
		UpstreamModelName: "GLM-4.6-Search",
		EnableThinking:    true,
		WebSearch:         true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search", "deep-web-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings["GLM-4.7"] = ModelMapping{
		DisplayName:       "GLM-4.7",
		UpstreamModelID:   "glm-4.7",
		UpstreamModelName: "GLM-4.7",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings["GLM-4.7-Thinking"] = ModelMapping{
		DisplayName:       "GLM-4.7-Thinking",
		UpstreamModelID:   "glm-4.7",
		UpstreamModelName: "GLM-4.7-Thinking",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings["GLM-4.7-Search"] = ModelMapping{
		DisplayName:       "GLM-4.7-Search",
		UpstreamModelID:   "glm-4.7",
		UpstreamModelName: "GLM-4.7-Search",
		EnableThinking:    true,
		WebSearch:         true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search", "deep-web-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings["GLM-4.5-V"] = ModelMapping{
		DisplayName:       "GLM-4.5-V",
		UpstreamModelID:   "glm-4.5v",
		UpstreamModelName: "GLM-4.5-V",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings["GLM-4.6-V"] = ModelMapping{
		DisplayName:       "GLM-4.6-V",
		UpstreamModelID:   "glm-4.6v",
		UpstreamModelName: "GLM-4.6-V",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search", "vlm-image-search", "vlm-image-recognition", "vlm-image-processing"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
		modelMappings["GLM-5"] = ModelMapping{
		DisplayName:       "GLM-5",
		UpstreamModelID:   "glm-5",
		UpstreamModelName: "GLM-5",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings["GLM-5-Thinking"] = ModelMapping{
		DisplayName:       "GLM-5-Thinking",
		UpstreamModelID:   "glm-5",
		UpstreamModelName: "GLM-5-Thinking",
		EnableThinking:    true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
	modelMappings["GLM-5-Search"] = ModelMapping{
		DisplayName:       "GLM-5-Search",
		UpstreamModelID:   "glm-5",
		UpstreamModelName: "GLM-5-Search",
		EnableThinking:    true,
		WebSearch:         true,
		AutoWebSearch:     true,
		MCPServers:        []string{"advanced-search", "deep-web-search"},
		OwnedBy:           "z.ai",
		IsBuiltin:         true,
	}
}
func GetModelMapping(modelID string) (ModelMapping, bool) {
	baseModel, enableThinking, enableSearch := ParseModelName(modelID)
	mappingsLock.RLock()
	defer mappingsLock.RUnlock()
	if mapping, ok := modelMappings[baseModel]; ok {
		if enableThinking {
			mapping.EnableThinking = true
		}
		if enableSearch {
			mapping.WebSearch = true
			mapping.AutoWebSearch = true
		}
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
	token, err := GetAnonymousToken()
	if err != nil {
		LogDebug("Failed to get token for model fetching: %v", err)
		return
	}
	req, err := http.NewRequest("GET", "https://chat.z.ai/api/models", nil)
	if err != nil {
		LogDebug("Failed to create model request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
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

// supportedDynamicModelPrefixes 支持的动态模型前缀（上游 API v2 兼容）
var supportedDynamicModelPrefixes = []string{
	"glm-4-",  // 如 glm-4-xxx
	"glm-4.5", // 如 glm-4.5-xxx
	"glm-4.6", // 如 glm-4.6-xxx
	"glm-4.7", // 如 glm-4.7-xxx
	"0727-",   // 如 0727-360B-API
	"0808-",   // 如 0808-360B-DR
}

// isSupportedDynamicModel 检查模型ID是否为支持的动态模型格式
func isSupportedDynamicModel(modelID string) bool {
	modelIDLower := strings.ToLower(modelID)
	for _, prefix := range supportedDynamicModelPrefixes {
		if strings.HasPrefix(modelIDLower, prefix) {
			return true
		}
	}
	return false
}

// updateDynamicMappings 更新动态模型映射
func updateDynamicMappings(models []ZAIModel) {
	mappingsLock.Lock()
	defer mappingsLock.Unlock()

	// 获取已被内置映射使用的上游模型ID
	usedUpstreamIDs := make(map[string]bool)
	for _, m := range modelMappings {
		if m.IsBuiltin {
			usedUpstreamIDs[m.UpstreamModelID] = true
		}
	}

	// 只为未被映射的模型添加动态映射
	for _, model := range models {
		// 只保留 glm 开头的模型（不区分大小写）
		modelIDLower := strings.ToLower(model.ID)
		if !strings.HasPrefix(modelIDLower, "glm") {
			continue
		}
		// 跳过已有内置映射的模型
		if _, exists := modelMappings[model.ID]; exists {
			continue
		}
		// 跳过已被映射的上游模型
		if usedUpstreamIDs[model.ID] {
			continue
		}
		// 跳过不支持的动态模型格式（避免 405 错误）
		if !isSupportedDynamicModel(model.ID) {
			LogDebug("Skipping unsupported dynamic model: %s", model.ID)
			continue
		}

		// 设置 owned_by 默认值
		ownedBy := model.OwnedBy
		if ownedBy == "" || ownedBy == "openai" {
			ownedBy = "z.ai"
		}

		// 设置显示名称
		displayName := model.Name
		if displayName == "" {
			displayName = model.ID
		}

		modelMappings[model.ID] = ModelMapping{
			DisplayName:       displayName,
			UpstreamModelID:   model.ID,
			UpstreamModelName: displayName,
			OwnedBy:           ownedBy,
			IsBuiltin:         false,
		}
	}
}

// modelsWithSuffixSupport 支持后缀组合的基础模型
var modelsWithSuffixSupport = map[string]bool{
	"GLM-4.5":     true,
	"GLM-4.6":     true,
	"GLM-4.7":     true,
	"GLM-4.5-V":   true,
	"GLM-4.6-V":   true,
	"GLM-4.5-Air": true,
}

// modelSuffixes 可用的后缀组合
var modelSuffixes = []string{
	"",                 // 基础
	"-thinking",        // 思考
	"-search",          // 搜索
	"-thinking-search", // 思考+搜索
}

// GetAvailableModels 获取所有可用模型
func GetAvailableModels() []ModelInfo {
	mappingsLock.RLock()
	defer mappingsLock.RUnlock()

	addedModels := make(map[string]bool)
	var models []ModelInfo

	// 为支持后缀的模型生成所有组合
	for baseModel := range modelsWithSuffixSupport {
		if m, ok := modelMappings[baseModel]; ok {
			for _, suffix := range modelSuffixes {
				modelID := baseModel + suffix
				if !addedModels[modelID] {
					addedModels[modelID] = true
					models = append(models, ModelInfo{
						ID:      modelID,
						Object:  "model",
						OwnedBy: m.OwnedBy,
					})
				}
			}
		}
	}

	// 添加其他已映射的模型
	for id, m := range modelMappings {
		if !addedModels[id] {
			addedModels[id] = true
			models = append(models, ModelInfo{
				ID:      id,
				Object:  "model",
				OwnedBy: m.OwnedBy,
			})
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
