# zai2api

将 Z.AI 转换为 OpenAI 兼容 API 的代理服务。

[![Build](https://github.com/XxxXTeam/zai2api/actions/workflows/build.yml/badge.svg)](https://github.com/XxxXTeam/zai2api/actions/workflows/build.yml)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

## 功能特性

- **OpenAI 兼容 API** - 支持 `/v1/chat/completions` 和 `/v1/models` 端点
- **多模型支持** - GLM-4.5、GLM-4.5-Thinking、GLM-4.5-Search、GLM-4.5-Air 等
- **流式响应** - 支持 SSE 流式输出
- **工具调用** - 支持 Function Calling
- **多模态** - 支持图片输入
- **思考模式** - 支持 Thinking 模型的思考过程处理
- **Token 管理** - 自动管理和轮换 Token
- **遥测统计** - 请求计数、Token 统计、成功率等

## 快速开始

### 从 Release 下载

前往 [Releases](https://github.com/XxxXTeam/zai2api/releases) 下载对应平台的二进制文件。

### 从源码构建

```bash
git clone https://github.com/XxxXTeam/zai2api.git
cd zai2api
go build -o zai2api ./cmd/main.go
```

### 配置

复制配置文件并修改：

```bash
cp .env.example .env
```

编辑 `.env` 文件，设置必要的配置项：

```env
PORT=8000
AUTH_TOKEN=your-api-key
```

### 运行

```bash
./zai2api
```

服务将在 `http://localhost:8000` 启动。

## API 端点

| 端点 | 方法 | 描述 |
|------|------|------|
| `/` | GET | 服务状态和遥测数据 |
| `/v1/models` | GET | 获取可用模型列表 |
| `/v1/chat/completions` | POST | 聊天补全接口 |

## 配置项

| 配置项 | 默认值 | 描述 |
|--------|--------|------|
| `PORT` | 8000 | 服务端口 |
| `AUTH_TOKEN` | - | API 认证令牌（支持多个，逗号分隔） |
| `BACKUP_TOKEN` | - | 备用令牌（用于多模态） |
| `DEBUG_LOGGING` | false | 调试日志 |
| `TOOL_SUPPORT` | true | 工具调用支持 |
| `THINKING_PROCESSING` | think | 思考过程处理：think/strip/raw |
| `LOG_LEVEL` | info | 日志级别：debug/info/warn/error |

完整配置请参考 [.env.example](.env.example)

## 使用示例

### cURL

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-api-key" \
  -d '{
    "model": "GLM-4.5",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

### Python (OpenAI SDK)

```python
from openai import OpenAI

client = OpenAI(
    api_key="your-api-key",
    base_url="http://localhost:8000/v1"
)

response = client.chat.completions.create(
    model="GLM-4.5",
    messages=[{"role": "user", "content": "Hello!"}]
)
print(response.choices[0].message.content)
```

## 支持的模型

- `GLM-4.5` - 主模型
- `GLM-4.5-Thinking` - 思考模型
- `GLM-4.5-Search` - 搜索增强模型
- `GLM-4.5-Air` - 轻量模型
- `GLM-4.6` 系列（新版）

## 项目结构

```
zai2api/
├── cmd/
│   ├── main.go           # 主程序入口
│   └── register/         # Token 注册工具
├── internal/
│   ├── chat.go           # 聊天补全处理
│   ├── config.go         # 配置管理
│   ├── models.go         # 模型定义
│   ├── token_manager.go  # Token 管理
│   ├── tools.go          # 工具调用
│   └── ...
├── .env.example          # 配置示例
└── README.md
```

## 许可证

本项目采用 [GNU General Public License v3.0](LICENSE) 许可证。
