// Package rest 提供带指数退避重试与可选限流的 HTTP 客户端。
//
// 分两层设计:
//   - HTTPConfig / NewHTTPClient:只管传输层参数(连接池、超时),不关心重试;
//   - Client:在传输层之上叠加 retryTransport(RoundTripper 中间件),
//     网络错误、429、5xx 统一在中间件里重试,Do 本身保持薄封装。
package rest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// HTTPConfig 是底层 http.Transport 的调优参数。
type HTTPConfig struct {
	DialTimeout         time.Duration // 建立 TCP 连接的超时
	KeepAlive           time.Duration // 空闲连接保活探测间隔
	MaxIdleConns        int           // 全局空闲连接上限
	MaxIdleConnsPerHost int           // 单个主机的空闲连接上限
	IdleConnTimeout     time.Duration // 空闲连接超过该时长未复用则回收
	TLSHandshakeTimeout time.Duration // TLS 握手超时
}

// DefaultHTTPConfig 返回适合调用大模型服务的默认值:
// 这类服务响应慢、请求不密集,所以超时偏宽、连接数偏保守。
func DefaultHTTPConfig() HTTPConfig {
	return HTTPConfig{
		DialTimeout:         5 * time.Second,
		KeepAlive:           30 * time.Second,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
}

// HTTPOption 修改 HTTPConfig 的一个字段。
type HTTPOption func(*HTTPConfig)

// WithDialTimeout 覆盖建连超时。
func WithDialTimeout(d time.Duration) HTTPOption {
	return func(c *HTTPConfig) { c.DialTimeout = d }
}

// WithMaxIdleConnsPerHost 覆盖单主机空闲连接上限。
func WithMaxIdleConnsPerHost(n int) HTTPOption {
	return func(c *HTTPConfig) { c.MaxIdleConnsPerHost = n }
}

// newTransport 按配置构造带连接池的 http.Transport。
func newTransport(cfg HTTPConfig) *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   cfg.DialTimeout,
			KeepAlive: cfg.KeepAlive,
		}).DialContext,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ExpectContinueTimeout: time.Second,
	}
}

// NewHTTPClient 构造一个不设整体超时的 *http.Client。
// 请求生命周期交给 context 控制,这样后续接入 SSE 长连接时,
// 不会被固定的 Client.Timeout 误杀。
func NewHTTPClient(opts ...HTTPOption) *http.Client {
	cfg := DefaultHTTPConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &http.Client{Transport: newTransport(cfg)}
}

// RetryConfig 控制指数退避重试的行为。
type RetryConfig struct {
	MaxRetries int           // 首次失败后最多重试的次数
	BaseDelay  time.Duration // 第一次重试前的等待时长,之后逐次翻倍
	MaxDelay   time.Duration // 单次等待时长上限
}

// DefaultRetryConfig 返回默认重试策略:最多重试 3 次,
// 等待从 500ms 起指数增长,封顶 10s。
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   10 * time.Second,
	}
}

// Limiter 是限流器的最小抽象:Wait 在放行前阻塞,ctx 取消时返回错误。
type Limiter interface {
	Wait(ctx context.Context) error
}

// Client 是带重试与限流的 HTTP 客户端,用法与 *http.Client 一致。
// 聚合了原始的httpClient 以及重试、限流配置，使得能力更丰富
type Client struct {
	http    *http.Client
	retry   RetryConfig
	limiter Limiter
}

// Option 定制 Client 的构造。
type Option func(*Client)

// WithRetry 覆盖默认的重试策略。
func WithRetry(cfg RetryConfig) Option {
	return func(c *Client) { c.retry = cfg }
}

// WithLimiter 挂接限流器,每次请求发送前先 Wait 放行。
func WithLimiter(l Limiter) Option {
	return func(c *Client) { c.limiter = l }
}

// WithHTTPOptions 覆盖底层连接池参数。
func WithHTTPOptions(opts ...HTTPOption) Option {
	return func(c *Client) { c.http = NewHTTPClient(opts...) }
}

// WithHTTPClient 注入自定义 *http.Client,主要用于测试。
// 注入的 Transport 仍会被 retryTransport 包装,重试行为不受影响。
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.http = client
		}
	}
}

// NewClient 构造 Client。重试与限流统一由 retryTransport 承载,
// Do 只需一次调用,不用自己维护重试循环。
func NewClient(opts ...Option) *Client {
	c := &Client{retry: DefaultRetryConfig()}
	for _, opt := range opts {
		opt(c)
	}
	c.retry = normalizeRetryConfig(c.retry)
	if c.http == nil {
		c.http = NewHTTPClient()
	}
	//  c.retry, c.limiter 底层会自动封装到用到的 http.RoundTripper里面，完了已经自定义实现了http.RoundTripper接口
	c.http = withRetryTransport(c.http, c.retry, c.limiter)
	return c
}

// Do 执行一次请求,网络错误、429、5xx 会自动重试(见 retryTransport)。
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("request 不能为空")
	}
	if c == nil || c.http == nil {
		return nil, errors.New("client 未初始化")
	}
	return c.http.Do(req)
}
