// Package metadata 定义与大模型交互的通用数据结构:
// 角色、消息、请求/响应报文,以及 Provider 抽象。
//
// 报文格式对齐 DeepSeek/OpenAI 兼容协议,上层调用方只需
// 面向 ChatRequest / ChatResponse 编程,不必关心厂商差异。
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Role 表示一条消息的发言者身份。
type Role string

const (
	RoleSystem    Role = "system"    // 系统提示词,设定模型整体行为
	RoleUser      Role = "user"      // 用户提问
	RoleAssistant Role = "assistant" // 模型回复
	RoleTool      Role = "tool"      // 工具调用返回的结果
)

// ChatMessage 是会话中的一条消息。
type ChatMessage struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 是一次对话请求。
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"` // 0 表示用模型默认值
	MaxTokens   int           `json:"max_tokens,omitempty"`  // 0 表示不限制
	Stream      bool          `json:"stream"`
}

// ChatResponse 是解析后的模型回复,usage 已拆成独立的可读字段。
type ChatResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// StreamChunk 是流式输出的一帧:要么是一段文本,要么是一次错误。
// 收到 Err 不为 nil 的帧后,应停止从 Channel 读取。
type StreamChunk struct {
	Content string
	Err     error
}

// Provider 抽象各家大模型服务端,便于后续接入不同厂商。
type Provider interface {
	// Name 返回服务商名称,如 "deepseek"。
	Name() string
	// Chat 发送一次非流式对话请求。
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// ChatStream 发送流式对话请求,返回逐帧输出的 Channel。
	ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
}

// chatResponsePayload 是服务端返回的原始报文(OpenAI 兼容格式)。
// 只声明解码需要的最小字段集,其余字段自动忽略。
type chatResponsePayload struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// DecodeChatResponse 从 HTTP 响应体解析出 ChatResponse。
// 只负责报文解析,不做状态码校验,调用方应先确认状态码为 2xx。
func DecodeChatResponse(r io.Reader) (*ChatResponse, error) {
	if r == nil {
		return nil, errors.New("响应体不能为空")
	}

	var payload chatResponsePayload
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析模型响应: %w", err)
	}
	if len(payload.Choices) == 0 {
		return nil, errors.New("模型响应中没有 choices")
	}

	return &ChatResponse{
		Content:      payload.Choices[0].Message.Content,
		InputTokens:  payload.Usage.PromptTokens,
		OutputTokens: payload.Usage.CompletionTokens,
		TotalTokens:  payload.Usage.TotalTokens,
	}, nil
}
