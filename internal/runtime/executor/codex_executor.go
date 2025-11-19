package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	codexauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var dataTag = []byte("data:")

// CodexExecutor is a stateless executor for Codex (OpenAI Responses API entrypoint).
// If api_key is unavailable on auth, it falls back to legacy via ClientAdapter.
type CodexExecutor struct {
	cfg *config.Config
}

func NewCodexExecutor(cfg *config.Config) *CodexExecutor { return &CodexExecutor{cfg: cfg} }

func (e *CodexExecutor) Identifier() string { return "codex" }

func (e *CodexExecutor) PrepareRequest(_ *http.Request, _ *cliproxyauth.Auth) error { return nil }

func (e *CodexExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	apiKey, baseURL := codexCreds(auth)

	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}
	reporter := newUsageReporter(ctx, e.Identifier(), req.Model, auth)
	defer reporter.trackFailure(ctx, &err)

	upstreamModel := util.ResolveOriginalModel(req.Model, req.Metadata)

	from := opts.SourceFormat
	to := sdktranslator.FromString("codex")
	body := sdktranslator.TranslateRequest(from, to, req.Model, bytes.Clone(req.Payload), false)
	body = applyReasoningEffortMetadata(body, req.Metadata, req.Model, "reasoning.effort")
	body = normalizeThinkingConfig(body, upstreamModel)
	if errValidate := validateThinkingConfig(body, upstreamModel); errValidate != nil {
		return resp, errValidate
	}
	body = applyPayloadConfig(e.cfg, req.Model, body)
	body, _ = sjson.SetBytes(body, "model", upstreamModel)
	body, _ = sjson.SetBytes(body, "stream", true)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")

	url := strings.TrimSuffix(baseURL, "/") + "/responses"
	httpReq, err := e.cacheHelper(ctx, from, url, req, body)
	if err != nil {
		return resp, err
	}
	applyCodexHeaders(httpReq, auth, apiKey)
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})
	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codex executor: close response body error: %v", errClose)
		}
	}()
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, b)
		// 将上游错误体转换为 Claude/Codex 标准错误 JSON, 统一以 200 返回
		msg := string(bytes.TrimSpace(b))
		if msg == "" {
			msg = fmt.Sprintf("upstream error: HTTP %d", httpResp.StatusCode)
		}
		translated := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, []byte(fmt.Sprintf("{\"type\":\"error\",\"message\":%q}", msg)), nil)
		return cliproxyexecutor.Response{Payload: []byte(translated)}, nil
	}
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	appendAPIResponseChunk(ctx, e.cfg, data)

	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}

		line = bytes.TrimSpace(line[5:])
		if gjson.GetBytes(line, "type").String() != "response.completed" {
			continue
		}

		if detail, ok := parseCodexUsage(line); ok {
			reporter.publish(ctx, detail)
		}

		var param any
		out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, line, &param)
		resp = cliproxyexecutor.Response{Payload: []byte(out)}
		return resp, nil
	}
	// 非流式未找到 response.completed 也视为上游问题, 包裹为错误响应而非抛异常
	errPayload := fmt.Sprintf("{\"type\":\"error\",\"message\":\"stream disconnected before completion\"}")
	translated := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, []byte(errPayload), nil)
	return cliproxyexecutor.Response{Payload: []byte(translated)}, nil
}

func (e *CodexExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (stream <-chan cliproxyexecutor.StreamChunk, err error) {
	apiKey, baseURL := codexCreds(auth)

	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}
	reporter := newUsageReporter(ctx, e.Identifier(), req.Model, auth)
	defer reporter.trackFailure(ctx, &err)

	upstreamModel := util.ResolveOriginalModel(req.Model, req.Metadata)

	from := opts.SourceFormat
	to := sdktranslator.FromString("codex")
	body := sdktranslator.TranslateRequest(from, to, req.Model, bytes.Clone(req.Payload), true)

	body = applyReasoningEffortMetadata(body, req.Metadata, req.Model, "reasoning.effort")
	body = normalizeThinkingConfig(body, upstreamModel)
	if errValidate := validateThinkingConfig(body, upstreamModel); errValidate != nil {
		return nil, errValidate
	}
	body = applyPayloadConfig(e.cfg, req.Model, body)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.SetBytes(body, "model", upstreamModel)

	url := strings.TrimSuffix(baseURL, "/") + "/responses"
	httpReq, err := e.cacheHelper(ctx, from, url, req, body)
	if err != nil {
		return nil, err
	}
	applyCodexHeaders(httpReq, auth, apiKey)
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		// 将上游错误包裹为 SSE 错误事件返回, 保持 HTTP 200 但以事件方式告知客户端
		b, _ := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()
		appendAPIResponseChunk(ctx, e.cfg, b)
		out := make(chan cliproxyexecutor.StreamChunk)
		go func() {
			defer close(out)
			// 若上游已是 SSE 片段, 直接转发; 否则包裹为标准 SSE error 事件
			line := bytes.TrimSpace(b)
			if bytes.HasPrefix(line, []byte("event:")) {
				if len(line) > 0 {
					out <- cliproxyexecutor.StreamChunk{Payload: append(line, '\n', '\n')}
				}
				return
			}
			payload := []byte("event: error\n")
			payload = append(payload, []byte("data: ")...)
			if len(line) == 0 {
				line = []byte(fmt.Sprintf("{\"type\":\"error\",\"message\":\"upstream %d\"}", httpResp.StatusCode))
			}
			payload = append(payload, line...)
			payload = append(payload, '\n', '\n')
			out <- cliproxyexecutor.StreamChunk{Payload: payload}
		}()
		return out, nil
	}
	out := make(chan cliproxyexecutor.StreamChunk)
	stream = out
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("codex executor: close response body error: %v", errClose)
			}
		}()
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800) // 50MB
		var param any
		for scanner.Scan() {
			line := scanner.Bytes()
			appendAPIResponseChunk(ctx, e.cfg, line)

			if bytes.HasPrefix(line, dataTag) {
				data := bytes.TrimSpace(line[5:])
				if gjson.GetBytes(data, "type").String() == "response.completed" {
					if detail, ok := parseCodexUsage(data); ok {
						reporter.publish(ctx, detail)
					}
				}
			}

			chunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, bytes.Clone(line), &param)
			for i := range chunks {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunks[i])}
			}
		}
		if errScan := scanner.Err(); errScan != nil {
			recordAPIResponseError(ctx, e.cfg, errScan)
			reporter.publishFailure(ctx)
			out <- cliproxyexecutor.StreamChunk{Err: errScan}
		}
	}()
	return stream, nil
}

func (e *CodexExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	// Codex 无原生 count_tokens 接口, 使用本地估算
	raw := opts.OriginalRequest
	if len(raw) == 0 {
		raw = req.Payload
	}
	count := util.EstimateTokensForModel(req.Model, raw)
	if count <= 0 {
		chars := utf8.RuneCount(raw)
		if chars < 0 {
			chars = len(raw)
		}
		count = chars / 4
		if count <= 0 {
			count = 1
		}
	}
	payload := []byte(fmt.Sprintf("{\"input_tokens\":%d}", count))
	return cliproxyexecutor.Response{Payload: payload}, nil
}

func (e *CodexExecutor) setReasoningEffortByAlias(modelName string, payload []byte) []byte {
	if util.InArray([]string{"gpt-5", "gpt-5-minimal", "gpt-5-low", "gpt-5-medium", "gpt-5-high"}, modelName) {
		payload, _ = sjson.SetBytes(payload, "model", "gpt-5")
		switch modelName {
		case "gpt-5-minimal":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "minimal")
		case "gpt-5-low":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "low")
		case "gpt-5-medium":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "medium")
		case "gpt-5-high":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "high")
		}
	} else if util.InArray([]string{"gpt-5-codex", "gpt-5-codex-low", "gpt-5-codex-medium", "gpt-5-codex-high"}, modelName) {
		payload, _ = sjson.SetBytes(payload, "model", "gpt-5-codex")
		switch modelName {
		case "gpt-5-codex-low":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "low")
		case "gpt-5-codex-medium":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "medium")
		case "gpt-5-codex-high":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "high")
		}
	} else if util.InArray([]string{"gpt-5-codex-mini", "gpt-5-codex-mini-medium", "gpt-5-codex-mini-high"}, modelName) {
		payload, _ = sjson.SetBytes(payload, "model", "gpt-5-codex-mini")
		switch modelName {
		case "gpt-5-codex-mini-medium":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "medium")
		case "gpt-5-codex-mini-high":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "high")
		}
	} else if util.InArray([]string{"gpt-5.1", "gpt-5.1-none", "gpt-5.1-low", "gpt-5.1-medium", "gpt-5.1-high"}, modelName) {
		payload, _ = sjson.SetBytes(payload, "model", "gpt-5.1")
		switch modelName {
		case "gpt-5.1-none":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "none")
		case "gpt-5.1-low":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "low")
		case "gpt-5.1-medium":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "medium")
		case "gpt-5.1-high":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "high")
		}
	} else if util.InArray([]string{"gpt-5.1-codex", "gpt-5.1-codex-low", "gpt-5.1-codex-medium", "gpt-5.1-codex-high"}, modelName) {
		payload, _ = sjson.SetBytes(payload, "model", "gpt-5.1-codex")
		switch modelName {
		case "gpt-5.1-codex-low":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "low")
		case "gpt-5.1-codex-medium":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "medium")
		case "gpt-5.1-codex-high":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "high")
		}
	} else if util.InArray([]string{"gpt-5.1-codex-mini", "gpt-5.1-codex-mini-medium", "gpt-5.1-codex-mini-high"}, modelName) {
		payload, _ = sjson.SetBytes(payload, "model", "gpt-5.1-codex-mini")
		switch modelName {
		case "gpt-5.1-codex-mini-medium":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "medium")
		case "gpt-5.1-codex-mini-high":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "high")
		}
	} else if util.InArray([]string{"gpt-5.1-codex-max", "gpt-5.1-codex-max-low", "gpt-5.1-codex-max-medium", "gpt-5.1-codex-max-high", "gpt-5.1-codex-max-xhigh"}, modelName) {
		payload, _ = sjson.SetBytes(payload, "model", "gpt-5.1-codex-max")
		switch modelName {
		case "gpt-5.1-codex-max-low":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "low")
		case "gpt-5.1-codex-max-medium":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "medium")
		case "gpt-5.1-codex-max-high":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "high")
		case "gpt-5.1-codex-max-xhigh":
			payload, _ = sjson.SetBytes(payload, "reasoning.effort", "xhigh")
		}
	}
	return payload
}

func (e *CodexExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Debugf("codex executor: refresh called")
	if auth == nil {
		return nil, fmt.Errorf("codex executor: auth is nil")
	}
	var refreshToken string
	if auth.Metadata != nil {
		if v, ok := auth.Metadata["refresh_token"].(string); ok && v != "" {
			refreshToken = v
		}
	}
	if refreshToken == "" {
		return auth, nil
	}
	svc := codexauth.NewCodexAuth(e.cfg)
	td, err := svc.RefreshTokensWithRetry(ctx, refreshToken, 3)
	if err != nil {
		return nil, err
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["id_token"] = td.IDToken
	auth.Metadata["access_token"] = td.AccessToken
	if td.RefreshToken != "" {
		auth.Metadata["refresh_token"] = td.RefreshToken
	}
	if td.AccountID != "" {
		auth.Metadata["account_id"] = td.AccountID
	}
	auth.Metadata["email"] = td.Email
	// Use unified key in files
	auth.Metadata["expired"] = td.Expire
	auth.Metadata["type"] = "codex"
	now := time.Now().Format(time.RFC3339)
	auth.Metadata["last_refresh"] = now
	return auth, nil
}

func (e *CodexExecutor) cacheHelper(ctx context.Context, from sdktranslator.Format, url string, req cliproxyexecutor.Request, rawJSON []byte) (*http.Request, error) {
	var cache codexCache
	if from == "claude" {
		userIDResult := gjson.GetBytes(req.Payload, "metadata.user_id")
		if userIDResult.Exists() {
			var hasKey bool
			key := fmt.Sprintf("%s-%s", req.Model, userIDResult.String())
			if cache, hasKey = codexCacheMap[key]; !hasKey || cache.Expire.Before(time.Now()) {
				cache = codexCache{
					ID:     uuid.New().String(),
					Expire: time.Now().Add(1 * time.Hour),
				}
				codexCacheMap[key] = cache
			}
		}
	} else if from == "openai-response" {
		promptCacheKey := gjson.GetBytes(req.Payload, "prompt_cache_key")
		if promptCacheKey.Exists() {
			cache.ID = promptCacheKey.String()
		}
	}

	rawJSON, _ = sjson.SetBytes(rawJSON, "prompt_cache_key", cache.ID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Conversation_id", cache.ID)
	httpReq.Header.Set("Session_id", cache.ID)
	return httpReq, nil
}

func applyCodexHeaders(r *http.Request, auth *cliproxyauth.Auth, token string) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)

	var ginHeaders http.Header
	if ginCtx, ok := r.Context().Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
		ginHeaders = ginCtx.Request.Header
	}

	misc.EnsureHeader(r.Header, ginHeaders, "Version", "0.21.0")
	misc.EnsureHeader(r.Header, ginHeaders, "Openai-Beta", "responses=experimental")
	misc.EnsureHeader(r.Header, ginHeaders, "Session_id", uuid.NewString())
	misc.EnsureHeader(r.Header, ginHeaders, "User-Agent", "codex_cli_rs/0.50.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464")

	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("Connection", "Keep-Alive")

	isAPIKey := false
	if auth != nil && auth.Attributes != nil {
		if v := strings.TrimSpace(auth.Attributes["api_key"]); v != "" {
			isAPIKey = true
		}
	}
	if !isAPIKey {
		r.Header.Set("Originator", "codex_cli_rs")
		if auth != nil && auth.Metadata != nil {
			if accountID, ok := auth.Metadata["account_id"].(string); ok {
				r.Header.Set("Chatgpt-Account-Id", accountID)
			}
		}
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(r, attrs)
}

func codexCreds(a *cliproxyauth.Auth) (apiKey, baseURL string) {
	if a == nil {
		return "", ""
	}
	if a.Attributes != nil {
		apiKey = a.Attributes["api_key"]
		baseURL = a.Attributes["base_url"]
	}
	if apiKey == "" && a.Metadata != nil {
		if v, ok := a.Metadata["access_token"].(string); ok {
			apiKey = v
		}
	}
	return
}

// httpStatusError 是一个携带HTTP状态码的错误类型
type httpStatusError struct {
	statusCode int
	err        error
}

func (e *httpStatusError) Error() string {
	return e.err.Error()
}

func (e *httpStatusError) StatusCode() int {
	return e.statusCode
}
