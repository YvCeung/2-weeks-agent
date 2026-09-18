// terminal 里的编排逻辑:把配置、报文类型(metadata)、带重试的
// HTTP 客户端(rest)组装成一次完整的模型调用,并把结果打印到终端。
// 支持单轮问答(run)与循环提问(runLoop)两种模式。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"YvCeung/2-weeks-agent/internal/config"
	"YvCeung/2-weeks-agent/internal/llm/metadata"
	"YvCeung/2-weeks-agent/internal/rest"
)

// requestTimeout 单次模型调用的整体超时。
const requestTimeout = 2 * time.Minute

// exitCommands 交互模式下主动结束对话的指令。
var exitCommands = map[string]bool{
	"exit":  true,
	"quit":  true,
	"q":     true,
	"/exit": true,
}

// httpDoer 是执行 HTTP 请求的最小抽象,便于测试时注入假客户端。
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// run 单轮模式:命令行直接给出问题,问完一轮即结束。
func run(parent context.Context, cfg *config.Config, question string) error {
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()

	messages := []metadata.ChatMessage{
		{Role: metadata.RoleUser, Content: strings.TrimSpace(question)},
	}
	resp, err := callModel(ctx, rest.NewClient(), cfg, messages)
	if err != nil {
		return err
	}
	printAnswer(resp)
	return nil
}

// runLoop 交互模式:循环读取提问,直到输入 exit/quit/q、Ctrl+D 或 Ctrl+C。
// 每轮问答追加进历史消息,让模型记住前面的上下文,形成一个连续会话。
func runLoop(parent context.Context, cfg *config.Config) error {
	client := rest.NewClient()

	fmt.Fprintln(os.Stdout, "进入交互式提问,输入 exit / quit / q 退出(Ctrl+D 亦可)。")

	// 单独开 goroutine 读 stdin,否则 Ctrl+C 时主流程会一直阻塞在读取上。
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-parent.Done():
				return
			}
		}
	}()

	history := make([]metadata.ChatMessage, 0, 32)
	for {
		fmt.Fprint(os.Stdout, "> ")
		line, ok := readLine(parent, lines)
		if !ok {
			fmt.Fprintln(os.Stdout) // ctx 取消或 EOF,补个换行
			return nil
		}
		if isExitCommand(line) {
			fmt.Fprintln(os.Stdout, "再见!")
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		history = append(history, metadata.ChatMessage{Role: metadata.RoleUser, Content: line})

		ctx, cancel := context.WithTimeout(parent, requestTimeout)
		resp, err := callModel(ctx, client, cfg, history)
		cancel()
		if err != nil {
			// 单轮出错不结束会话;把本轮问题移出历史,保持上下文干净
			history = history[:len(history)-1]
			fmt.Fprintln(os.Stderr, "出错:", err)
			continue
		}

		history = append(history, metadata.ChatMessage{Role: metadata.RoleAssistant, Content: resp.Content})
		printAnswer(resp)
	}
}

// readLine 阻塞读取一行输入;ctx 取消或 stdin 关闭(EOF)时返回 ok=false。
func readLine(ctx context.Context, lines <-chan string) (string, bool) {
	select {
	case <-ctx.Done():
		return "", false
	case line, ok := <-lines:
		return line, ok
	}
}

// isExitCommand 判断输入是否为退出指令(不区分大小写)。
func isExitCommand(line string) bool {
	return exitCommands[strings.ToLower(strings.TrimSpace(line))]
}

// printAnswer 打印模型回复与 token 统计。
func printAnswer(resp *metadata.ChatResponse) {
	fmt.Fprintln(os.Stdout, resp.Content)
	fmt.Fprintf(
		os.Stdout,
		"\ntoken: input=%d output=%d total=%d\n",
		resp.InputTokens,
		resp.OutputTokens,
		resp.TotalTokens,
	)
}

// callModel 用 metadata 的报文结构与 rest 的带重试客户端发起一次非流式对话。
func callModel(
	ctx context.Context,
	client httpDoer,
	cfg *config.Config,
	messages []metadata.ChatMessage,
) (*metadata.ChatResponse, error) {
	if client == nil {
		return nil, errors.New("HTTP 客户端不能为空")
	}
	if cfg == nil || cfg.LLM.APIKey == "" {
		return nil, errors.New("llm.api_key 未配置(configs/config.yaml 或 LLM_API_KEY 环境变量)")
	}
	parsed, err := url.Parse(cfg.LLM.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("llm.base_url 不是有效的 HTTP(S) 地址: %q", cfg.LLM.BaseURL)
	}
	if len(messages) == 0 {
		return nil, errors.New("消息列表不能为空")
	}

	payload := metadata.ChatRequest{
		Model:    cfg.LLM.Model,
		Messages: messages,
		Stream:   false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化模型请求: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.LLM.BaseURL+"/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("创建模型请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.LLM.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("模型调用被取消: %w", ctx.Err())
		}
		return nil, fmt.Errorf("调用模型接口: %w", err)
	}
	defer resp.Body.Close()

	// rest.Client 已对网络错误、429、5xx 做过重试,到这里仍非 2xx 的
	// 一般是 4xx(如鉴权失败、参数错误),直接报错不再重试。
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if readErr != nil {
			return nil, fmt.Errorf("模型接口返回 %s,且读取错误响应失败: %w", resp.Status, readErr)
		}
		message := strings.TrimSpace(string(raw))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("模型接口返回 %s: %s", resp.Status, message)
	}

	return metadata.DecodeChatResponse(resp.Body)
}
