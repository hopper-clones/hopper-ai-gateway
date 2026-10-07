package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

// This uses real loopback WebSocket connections; it does not qualify a hosted provider.
func TestCodexTransportCacheAccountReuseContinuationAndReconnect(t *testing.T) {
	type observed struct {
		connection           int64
		token, previous, key string
	}
	requests := make(chan observed, 8)
	upstreamConnections := make(chan *websocket.Conn, 8)
	var connections atomic.Int64
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		id := connections.Add(1)
		upstreamConnections <- conn
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			requests <- observed{id, r.Header.Get("Authorization"), gjson.GetBytes(body, "previous_response_id").String(), gjson.GetBytes(body, "prompt_cache_key").String()}
			terminal := []byte(`{"type":"response.completed","response":{"id":"resp-local","object":"response","status":"completed","model":"gpt-5-codex","output":[],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":40}}}}`)
			if err := conn.WriteMessage(websocket.TextMessage, terminal); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	}))
	defer server.Close()
	cfg := &config.Config{SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll}}
	executor := NewCodexAutoExecutor(cfg)
	executor.wsExec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	const session = "qualification-thread"
	defer executor.wsExec.CloseExecutionSession(session)
	alias := t.Name()
	capture := &codexResponseModelUsageCapture{alias: alias, records: make(chan coreusage.Record, 8)}
	coreusage.RegisterNamedPlugin(alias, capture)
	defer coreusage.RegisterNamedPlugin(alias, codexResponseModelNoopUsagePlugin{})
	ctx := cliproxyexecutor.WithDownstreamWebsocket(coreusage.WithRequestedModelAlias(context.Background(), alias))
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: session}}
	call := func(account, previous string) observed {
		t.Helper()
		auth := &cliproxyauth.Auth{ID: account, Attributes: map[string]string{"api_key": account, "base_url": server.URL, "websockets": "true"}}
		payload := []byte(fmt.Sprintf(`{"model":"gpt-5-codex","input":"hello","prompt_cache_key":"thread-cache","previous_response_id":%q}`, previous))
		result, err := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "gpt-5-codex", Payload: payload}, opts)
		if err != nil {
			t.Fatalf("ExecuteStream: %v", err)
		}
		for chunk := range result.Chunks {
			if chunk.Err != nil {
				t.Fatalf("stream: %v", chunk.Err)
			}
		}
		got := <-requests
		record := capture.await(t)
		if record.AuthID != account || record.Failed || record.Detail.CacheReadTokens != 30 || record.Detail.CacheCreationTokens != 40 || record.Detail.TotalTokens != 120 {
			t.Fatalf("account/cache usage: %+v", record)
		}
		if got.token != "Bearer "+account || got.previous != previous || got.key != "thread-cache" {
			t.Fatalf("upstream request: %+v", got)
		}
		return got
	}
	first := call("account-a", "")
	second := call("account-a", "resp-local")
	if second.connection != first.connection {
		t.Fatal("same-account continuation opened a new connection")
	}
	switched := call("account-b", "")
	if switched.connection == first.connection {
		t.Fatal("account switch reused another account's connection")
	}
	_ = (<-upstreamConnections).Close() // The old account connection is already detached.
	_ = (<-upstreamConnections).Close() // Disconnect the current account at the real upstream.
	select {
	case <-executor.wsExec.UpstreamDisconnectChan(session):
	case <-time.After(5 * time.Second):
		t.Fatal("upstream disconnect was not observed")
	}
	reconnected := call("account-b", "resp-local")
	if reconnected.connection == switched.connection {
		t.Fatal("upstream-disconnected session did not reconnect")
	}
}

func TestCodexWebsocketChatCacheKeyMatchesHTTP(t *testing.T) {
	req := cliproxyexecutor.Request{Model: "gpt-5-codex", Payload: []byte(`{"prompt_cache_key":"explicit-thread-cache"}`)}
	body, headers, err := applyCodexPromptCacheHeadersWithContext(context.Background(), sdktranslator.FormatOpenAI, req, []byte(`{"model":"gpt-5-codex"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(body, "prompt_cache_key").String(); got != "explicit-thread-cache" {
		t.Fatalf("cache key = %q", got)
	}
	if got := headerValueCaseInsensitive(headers, "session_id"); got != "explicit-thread-cache" {
		t.Fatalf("session header = %q", got)
	}
}

func TestCodexWebsocketChatCacheFallbackMatchesHTTP(t *testing.T) {
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Set("userApiKey", "qualification-client-key")
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	req := cliproxyexecutor.Request{Model: "gpt-5-codex", Payload: []byte(`{"model":"gpt-5-codex"}`)}
	raw := []byte(`{"model":"gpt-5-codex"}`)
	httpReq, httpBody, err := NewCodexExecutor(nil).cacheHelper(ctx, sdktranslator.FormatOpenAI, "https://example.com/responses", req, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer httpReq.Body.Close()
	wsBody, _, err := applyCodexPromptCacheHeadersWithContext(ctx, sdktranslator.FormatOpenAI, req, raw)
	if err != nil {
		t.Fatal(err)
	}
	httpKey := gjson.GetBytes(httpBody, "prompt_cache_key").String()
	wsKey := gjson.GetBytes(wsBody, "prompt_cache_key").String()
	if httpKey == "" || wsKey != httpKey {
		t.Fatalf("HTTP cache key %q != WS cache key %q", httpKey, wsKey)
	}
}
