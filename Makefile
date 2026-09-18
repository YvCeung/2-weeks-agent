# 项目常用命令。默认执行 make help。

GO     := go
BINARY := bin/2-weeks-agent

.PHONY: help build run test vet fmt tidy clean

## help: 列出所有可用命令
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s:

## build: 编译二进制到 bin/2-weeks-agent
build:
	$(GO) build -o $(BINARY) ./cmd

## build-win: 交叉编译 Windows 64 位可执行程序 bin/2-weeks-agent.exe
build-win:
	GOOS=windows GOARCH=amd64 $(GO) build -o bin/2-weeks-agent.exe ./cmd

## run: 运行程序。带问题则单轮问答: make run q="你好";不带问题则进入交互式提问
run:
	$(GO) run ./cmd $(q)

## test: 运行单元测试
test:
	$(GO) test ./...

## vet: 静态检查
vet:
	$(GO) vet ./...

## fmt: 格式化代码
fmt:
	$(GO) fmt ./...

## tidy: 整理 go.mod / go.sum
tidy:
	$(GO) mod tidy

## clean: 删除构建产物
clean:
	rm -rf bin/
