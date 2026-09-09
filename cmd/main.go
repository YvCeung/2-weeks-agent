package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"YvCeung/2-weeks-agent/internal/config"
)

// https://api-docs.deepseek.com/zh-cn/

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	apiKey := cfg.LLM.APIKey
	if apiKey == "" {
		log.Fatal("请设置 configs/config.yaml 中的 llm.api_key,或 LLM_SECRET_KEY 环境变量")
	}

	question := "你是谁，能帮我做哪些东西"
	payload := ChatRequest{
		Model: cfg.LLM.Model,
		Messages: []Message{
			{Role: "user", Content: question},
		},
		Stream: false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.LLM.BaseURL+"/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		log.Fatalf("unexpected status code %d (%s)", resp.StatusCode, string(raw))
	}

	var result ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Fatal(err)
	}
	if len(result.Choices) == 0 {
		log.Fatal("No choices found")
	}

	fmt.Println(result.Choices[0].Message.Content)
	fmt.Printf(
		"token: input=%d output=%d total=%d\n",
		result.Usage.PromptTokens,
		result.Usage.CompletionTokens,
		result.Usage.TotalTokens,
	)
}
