package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"YvCeung/2-weeks-agent/internal/config"
)

// main 只负责程序入口:注册退出信号、解析参数、加载配置,
// 模型调用的编排逻辑在 terminal.go 中。
func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	question := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(2)
	}

	// 命令行带问题则单轮问答;不带问题进入交互式循环提问。
	var runErr error
	if question != "" {
		runErr = run(ctx, cfg, question)
	} else {
		runErr = runLoop(ctx, cfg)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "\n出错:", runErr)
		os.Exit(1)
	}
}
