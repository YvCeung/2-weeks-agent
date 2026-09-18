package rest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// retryTransport 实现了go标准库提供的RoundTripper接口，用于实现重试能力
// 每次 RoundTrip 先过限流器,再对网络错误、429、5xx 按退避策略重试。
// 重试逻辑收口在这里之后,Client.Do 对于上层业务来说只是一次普通调用。
type retryTransport struct {
	base    http.RoundTripper
	retry   RetryConfig
	limiter Limiter // 可空,不设限流器时直接放行
}

// withRetryTransport 把普通 *http.Client 包装成带重试/限流的客户端,
// Transport 之外的配置(CheckRedirect、Timeout 等)原样保留。
func withRetryTransport(cli *http.Client, retry RetryConfig, limiter Limiter) *http.Client {
	base := cli.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{
		Transport:     &retryTransport{base: base, retry: retry, limiter: limiter},
		CheckRedirect: cli.CheckRedirect,
		Timeout:       cli.Timeout,
		Jar:           cli.Jar,
	}
}

// NewRetryHTTPClient 返回一个自带重试/限流的 *http.Client,
// 适合想直接用 http.Client 接口、不引入 Client 类型的场景。
func NewRetryHTTPClient(retry RetryConfig, limiter Limiter, opts ...HTTPOption) *http.Client {
	return withRetryTransport(NewHTTPClient(opts...), normalizeRetryConfig(retry), limiter)
}

// RoundTrip 实现 http.RoundTripper。
func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	// RoundTripper 契约:本层负责关闭传入的 Body;
	// 重试时通过 GetBody 重建,不依赖已被消费的原始 Body。
	if req.Body != nil {
		defer req.Body.Close()
	}
	if t.limiter != nil {
		if err := t.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("等待限流器放行时出错: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= t.retry.MaxRetries; attempt++ {
		r, err := requestForAttempt(req, attempt)
		if err != nil {
			return nil, err
		}

		resp, err := t.base.RoundTrip(r)
		if err == nil && !retryableStatus(resp.StatusCode) {
			// 正常响应或不可重试的 4xx,直接交给调用方
			return resp, nil
		}

		var wait time.Duration
		if err != nil {
			// 传输层错误:ctx 已取消说明重试没有意义,直接返回
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			wait = backoffDelay(attempt, t.retry)
		} else {
			// 429/5xx:优先尊重服务端 Retry-After,其次用退避策略
			wait = retryAfter(resp)
			if wait <= 0 {
				wait = backoffDelay(attempt, t.retry)
			}
			// 把剩余 Body 读完再关,连接才能回到池中复用
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("服务端返回 %s", resp.Status)
		}

		if attempt == t.retry.MaxRetries {
			break
		}
		if !sleepCtx(ctx, wait) {
			return nil, ctx.Err()
		}
	}

	return nil, fmt.Errorf("重试 %d 次后仍失败: %w", t.retry.MaxRetries, lastErr)
}

// requestForAttempt 返回第 attempt 次尝试要用的请求:
// 首次直接用原请求;重试时必须 Clone,因为原请求的 Body
// 可能已被上一轮消费,需要 GetBody 重建一份。
func requestForAttempt(req *http.Request, attempt int) (*http.Request, error) {
	if attempt == 0 {
		return req, nil
	}
	if req.Body == nil {
		return req.Clone(req.Context()), nil
	}
	if req.GetBody == nil {
		return nil, errors.New("请求体无法重放,不能安全重试")
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("重建请求体: %w", err)
	}
	clone := req.Clone(req.Context())
	clone.Body = body
	return clone, nil
}

// retryableStatus 判断状态码是否值得重试:429 或 5xx。
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// normalizeRetryConfig 把零值/负值/越界的重试参数收敛到合理范围。
func normalizeRetryConfig(cfg RetryConfig) RetryConfig {
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.BaseDelay < 0 {
		cfg.BaseDelay = 0
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = cfg.BaseDelay
	}
	if cfg.MaxDelay < cfg.BaseDelay {
		cfg.MaxDelay = cfg.BaseDelay
	}
	return cfg
}

// backoffDelay 计算第 attempt 次重试前的等待时长:
// 从 BaseDelay 起逐次翻倍、封顶 MaxDelay,再叠加 ±10% 抖动,
// 避免多个客户端同时重试形成"惊群"。
func backoffDelay(attempt int, cfg RetryConfig) time.Duration {
	if cfg.BaseDelay <= 0 {
		return 0
	}

	delay := cfg.BaseDelay
	for i := 0; i < attempt; i++ {
		if delay >= cfg.MaxDelay {
			break
		}
		delay *= 2
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}

	jitter := delay / 10
	if jitter <= 0 {
		return delay
	}
	// 最终落在 [0.9*delay, 1.1*delay) 区间
	return delay - jitter + time.Duration(rand.Int63n(int64(2*jitter)))
}

// retryAfter 解析响应头 Retry-After(秒数或 HTTP 日期)。
func retryAfter(resp *http.Response) time.Duration {
	return retryAfterAt(resp, time.Now())
}

// retryAfterAt 是 retryAfter 的可注入时间版本,便于测试。
func retryAfterAt(resp *http.Response, now time.Time) time.Duration {
	if resp == nil {
		return 0
	}
	value := resp.Header.Get("Retry-After")
	if value == "" {
		return 0
	}
	// 优先按秒数解析
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	// 兼容 HTTP 日期格式,只取未来时间
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := retryAt.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

// sleepCtx 等待 delay,ctx 先取消则返回 false(未等到)。
func sleepCtx(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
