package internal

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function,omitempty"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}
type ToolCall struct {
	Index    int              `json:"index,omitempty"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

var (
	toolCallFencePattern  = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")
	functionCallPattern   = regexp.MustCompile(`(?s)调用函数\s*[：:]\s*([\w\-\.]+)\s*(?:参数|arguments)[：:]\s*(\{.*?\})`)
	singleFunctionPattern = regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*"([^"]+)"\s*,\s*"arguments"\s*:\s*(\{[^}]*\}|"[^"]*")\s*\}`)
)

func GenerateToolPrompt(tools []Tool, toolChoice interface{}) string {
	if len(tools) == 0 {
		return ""
	}
	var toolDefs []string
	var toolNames []string
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}

		fn := tool.Function
		toolNames = append(toolNames, fn.Name)
		toolInfo := fmt.Sprintf("### %s\n%s", fn.Name, fn.Description)
		if len(fn.Parameters) > 0 {
			var params struct {
				Type       string                 `json:"type"`
				Properties map[string]interface{} `json:"properties"`
				Required   []string               `json:"required"`
			}
			if err := json.Unmarshal(fn.Parameters, &params); err == nil && len(params.Properties) > 0 {
				requiredSet := make(map[string]bool)
				for _, r := range params.Required {
					requiredSet[r] = true
				}

				toolInfo += "\n**参数:**"
				for name, details := range params.Properties {
					detailMap, _ := details.(map[string]interface{})
					paramType, _ := detailMap["type"].(string)
					paramDesc, _ := detailMap["description"].(string)
					enumVals, hasEnum := detailMap["enum"].([]interface{})

					reqMark := ""
					if requiredSet[name] {
						reqMark = " (必填)"
					}

					paramLine := fmt.Sprintf("\n- **%s** (%s%s): %s", name, paramType, reqMark, paramDesc)
					if hasEnum && len(enumVals) > 0 {
						var enumStrs []string
						for _, e := range enumVals {
							enumStrs = append(enumStrs, fmt.Sprintf("`%v`", e))
						}
						paramLine += fmt.Sprintf(" [可选值: %s]", strings.Join(enumStrs, ", "))
					}
					toolInfo += paramLine
				}
			}
		}
		toolDefs = append(toolDefs, toolInfo)
	}

	if len(toolDefs) == 0 {
		return ""
	}

	instructions := getToolChoiceInstructions(toolChoice, toolNames)
	return "\n\n# 可用工具\n" + strings.Join(toolDefs, "\n\n") + "\n\n" + instructions
}

func getToolChoiceInstructions(toolChoice interface{}, toolNames []string) string {
	baseInstructions := `# 工具调用格式
当需要调用工具时，请严格按照以下 JSON 格式输出：
` + "```json" + `
{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"函数名","arguments":"{\"参数名\":\"参数值\"}"}}]}
` + "```" + `
**重要规则：**
1. arguments 字段必须是 JSON 字符串（双引号包裹），不是对象
2. 调用工具时只输出 JSON，不要添加任何解释文字
3. 可以在 tool_calls 数组中同时调用多个工具`

	switch tc := toolChoice.(type) {
	case string:
		if tc == "auto" {
			return baseInstructions + "\n4. 根据用户需求自行判断是否需要调用工具"
		} else if tc == "required" {
			return baseInstructions + "\n4. **必须**调用至少一个工具来响应用户请求"
		}
	case map[string]interface{}:
		if tc["type"] == "function" {
			if fn, ok := tc["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok {
					return baseInstructions + fmt.Sprintf("\n4. **必须**调用 `%s` 工具来响应用户请求", name)
				}
			}
		}
	}

	return baseInstructions + "\n4. 根据用户需求自行判断是否需要调用工具"
}

func ProcessMessagesWithTools(messages []Message, tools []Tool, toolChoice interface{}) []Message {
	if !Cfg.ToolSupport || len(tools) == 0 {
		LogDebug("[Tools] Tool support disabled or no tools provided")
		return messages
	}
	if tc, ok := toolChoice.(string); ok && tc == "none" {
		LogDebug("[Tools] Tool choice is 'none', skipping tool processing")
		return messages
	}

	toolPrompt := GenerateToolPrompt(tools, toolChoice)
	if toolPrompt == "" {
		LogDebug("[Tools] Generated empty tool prompt")
		return messages
	}
	LogDebug("[Tools] Injecting tool prompt for %d tools", len(tools))

	processed := make([]Message, len(messages))
	copy(processed, messages)

	// 处理 tool 角色消息，转换为 user 消息
	for i, msg := range processed {
		if msg.Role == "tool" {
			processed[i] = convertToolMessage(msg)
		}
	}

	hasSystem := false
	for i, msg := range processed {
		if msg.Role == "system" {
			hasSystem = true
			processed[i].Content = appendTextToContent(msg.Content, toolPrompt)
			break
		}
	}
	if !hasSystem {
		systemMsg := Message{
			Role:    "system",
			Content: "你是一个智能助手，能够帮助用户完成各种任务。" + toolPrompt,
		}
		processed = append([]Message{systemMsg}, processed...)
	}

	return processed
}

func convertToolMessage(msg Message) Message {
	content, _ := msg.ParseContent()
	return Message{
		Role:    "user",
		Content: fmt.Sprintf("[工具调用结果]\n%s", content),
	}
}

func appendTextToContent(content interface{}, suffix string) interface{} {
	switch c := content.(type) {
	case string:
		return c + suffix
	case []interface{}:
		result := make([]interface{}, len(c))
		copy(result, c)
		lastTextIdx := -1
		for i, item := range result {
			if part, ok := item.(map[string]interface{}); ok {
				if partType, _ := part["type"].(string); partType == "text" {
					lastTextIdx = i
				}
			}
		}

		if lastTextIdx >= 0 {
			if part, ok := result[lastTextIdx].(map[string]interface{}); ok {
				newPart := make(map[string]interface{})
				for k, v := range part {
					newPart[k] = v
				}
				if text, ok := newPart["text"].(string); ok {
					newPart["text"] = text + suffix
				}
				result[lastTextIdx] = newPart
			}
		} else {
			result = append(result, map[string]interface{}{
				"type": "text",
				"text": suffix,
			})
		}
		return result
	default:
		return content
	}
}

// ExtractToolInvocations 从响应文本中提取工具调用
func ExtractToolInvocations(text string) []ToolCall {
	if text == "" {
		return nil
	}

	// 限制扫描范围
	scanText := text
	if len(scanText) > Cfg.ScanLimit {
		scanText = scanText[:Cfg.ScanLimit]
	}

	// 方法1: 从 JSON fence 中提取
	matches := toolCallFencePattern.FindAllStringSubmatch(scanText, -1)
	for _, match := range matches {
		if len(match) > 1 {
			if calls := parseToolCallsJSON(match[1]); calls != nil {
				LogDebug("[ExtractToolInvocations] Found %d tool calls in JSON fence", len(calls))
				return calls
			}
		}
	}

	// 方法2: 提取内联 JSON
	if calls := extractInlineToolCalls(scanText); calls != nil {
		LogDebug("[ExtractToolInvocations] Found %d tool calls inline", len(calls))
		return calls
	}

	// 方法3: 提取单个函数调用格式 {"name":"...","arguments":"..."}
	if calls := extractSingleFunctionCall(scanText); calls != nil {
		LogDebug("[ExtractToolInvocations] Found single function call")
		return calls
	}

	// 方法4: 解析自然语言函数调用
	if match := functionCallPattern.FindStringSubmatch(scanText); len(match) > 2 {
		funcName := strings.TrimSpace(match[1])
		argsStr := strings.TrimSpace(match[2])
		var args interface{}
		if err := json.Unmarshal([]byte(argsStr), &args); err == nil {
			LogDebug("[ExtractToolInvocations] Found natural language function call: %s", funcName)
			return []ToolCall{{
				ID:   generateCallID(),
				Type: "function",
				Function: ToolCallFunction{
					Name:      funcName,
					Arguments: argsStr,
				},
			}}
		}
	}

	return nil
}

func extractSingleFunctionCall(text string) []ToolCall {
	matches := singleFunctionPattern.FindStringSubmatch(text)
	if len(matches) < 3 {
		return nil
	}

	funcName := matches[1]
	argsRaw := matches[2]

	var argsStr string
	if strings.HasPrefix(argsRaw, "\"") {
		if err := json.Unmarshal([]byte(argsRaw), &argsStr); err != nil {
			argsStr = argsRaw
		}
	} else {
		argsStr = argsRaw
	}

	return []ToolCall{{
		ID:   generateCallID(),
		Type: "function",
		Function: ToolCallFunction{
			Name:      funcName,
			Arguments: argsStr,
		},
	}}
}

func parseToolCallsJSON(jsonStr string) []ToolCall {
	var data struct {
		ToolCalls []struct {
			ID       string      `json:"id"`
			Type     string      `json:"type"`
			Function interface{} `json:"function"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil
	}
	if len(data.ToolCalls) == 0 {
		return nil
	}
	var calls []ToolCall
	for _, tc := range data.ToolCalls {
		call := ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
		}
		if call.Type == "" {
			call.Type = "function"
		}
		if fn, ok := tc.Function.(map[string]interface{}); ok {
			call.Function.Name, _ = fn["name"].(string)
			if args, ok := fn["arguments"]; ok {
				switch v := args.(type) {
				case string:
					call.Function.Arguments = v
				case map[string]interface{}:
					if b, err := json.Marshal(v); err == nil {
						call.Function.Arguments = string(b)
					}
				}
			}
		}

		calls = append(calls, call)
	}

	return calls
}

func extractInlineToolCalls(text string) []ToolCall {
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}

		// 找匹配的右括号
		braceCount := 1
		inString := false
		escapeNext := false
		j := i + 1

		for j < len(text) && braceCount > 0 {
			if escapeNext {
				escapeNext = false
				j++
				continue
			}

			switch text[j] {
			case '\\':
				escapeNext = true
			case '"':
				if !escapeNext {
					inString = !inString
				}
			case '{':
				if !inString {
					braceCount++
				}
			case '}':
				if !inString {
					braceCount--
				}
			}
			j++
		}

		if braceCount == 0 {
			jsonStr := text[i:j]
			if calls := parseToolCallsJSON(jsonStr); calls != nil {
				return calls
			}
		}
	}

	return nil
}

func RemoveToolJSONContent(text string) string {
	result := toolCallFencePattern.ReplaceAllStringFunc(text, func(match string) string {
		submatch := toolCallFencePattern.FindStringSubmatch(match)
		if len(submatch) > 1 {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(submatch[1]), &data); err == nil {
				if _, ok := data["tool_calls"]; ok {
					return ""
				}
			}
		}
		return match
	})
	result = removeInlineToolCallJSON(result)

	return strings.TrimSpace(result)
}

func removeInlineToolCallJSON(text string) string {
	var result strings.Builder
	i := 0

	for i < len(text) {
		if text[i] != '{' {
			result.WriteByte(text[i])
			i++
			continue
		}
		braceCount := 1
		inString := false
		escapeNext := false
		j := i + 1

		for j < len(text) && braceCount > 0 {
			if escapeNext {
				escapeNext = false
				j++
				continue
			}

			switch text[j] {
			case '\\':
				escapeNext = true
			case '"':
				if !escapeNext {
					inString = !inString
				}
			case '{':
				if !inString {
					braceCount++
				}
			case '}':
				if !inString {
					braceCount--
				}
			}
			j++
		}

		if braceCount == 0 {
			jsonStr := text[i:j]
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &data); err == nil {
				if _, ok := data["tool_calls"]; ok {
					i = j
					continue
				}
			}
		}

		result.WriteByte(text[i])
		i++
	}

	return result.String()
}

func generateCallID() string {
	return fmt.Sprintf("call_%d", time.Now().UnixNano())
}
