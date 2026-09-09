package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// https://api-docs.deepseek.com/zh-cn/
const (
	baseUrlOpenai = "https://api.deepseek.com"
	modelName     = "deepseek-v4-pro"
)

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
	var apiKey string
	apiKey = os.Getenv("LLM_SECRET_KEY")
	if apiKey == "" {
		log.Fatal("Please set LLM_SECRET_KEY environment variable")
	}

	question := "你是谁，能帮我做哪些东西"
	payload := ChatRequest{
		Model: modelName,
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
		baseUrlOpenai+"/chat/completions",
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
