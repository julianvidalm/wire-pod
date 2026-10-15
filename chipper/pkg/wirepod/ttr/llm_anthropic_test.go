package wirepod_ttr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/kercre123/wire-pod/chipper/pkg/vars"
	"github.com/sashabaranov/go-openai"
)

func paramsJSON(t *testing.T, req openai.ChatCompletionRequest) map[string]any {
	t.Helper()
	raw, err := json.Marshal(anthropicParams(req))
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	return out
}

func TestAnthropicParamsMovesSystemPromptOutOfMessages(t *testing.T) {
	got := paramsJSON(t, openai.ChatCompletionRequest{
		Model: "claude-opus-5-5",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "You are Vector."},
			{Role: openai.ChatMessageRoleUser, Content: "hi"},
			{Role: openai.ChatMessageRoleAssistant, Content: "Hello!"},
			{Role: openai.ChatMessageRoleUser, Content: "what time is it"},
		},
	})

	system := got["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("system blocks = %d, want 1", len(system))
	}
	block := system[0].(map[string]any)
	if block["text"] != "You are Vector." {
		t.Errorf("system text = %q", block["text"])
	}
	if cc, ok := block["cache_control"].(map[string]any); !ok || cc["type"] != "ephemeral" {
		t.Errorf("system prompt is not cached: %v", block["cache_control"])
	}
	if got["model"] != "claude-opus-5-5" {
		t.Errorf("model = %v", got["model"])
	}

	msgs := got["messages"].([]any)
	wantRoles := []string{"user", "assistant", "user"}
	wantTexts := []string{"hi", "Hello!", "what time is it"}
	if len(msgs) != len(wantRoles) {
		t.Fatalf("messages = %d, want %d", len(msgs), len(wantRoles))
	}
	for i, m := range msgs {
		msg := m.(map[string]any)
		if msg["role"] != wantRoles[i] {
			t.Errorf("messages[%d].role = %v, want %s", i, msg["role"], wantRoles[i])
		}
		text := msg["content"].([]any)[0].(map[string]any)["text"]
		if text != wantTexts[i] {
			t.Errorf("messages[%d] text = %v, want %s", i, text, wantTexts[i])
		}
	}
}

func TestAnthropicParamsDefaultsAndOmitsSamplingParameters(t *testing.T) {
	got := paramsJSON(t, openai.ChatCompletionRequest{
		Temperature: 1,
		TopP:        1,
		Messages:    []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	})

	if got["model"] != defaultAnthropicModel {
		t.Errorf("model = %v, want %s", got["model"], defaultAnthropicModel)
	}
	if got["max_tokens"] != float64(defaultAnthropicMaxTokens) {
		t.Errorf("max_tokens = %v, want %d", got["max_tokens"], defaultAnthropicMaxTokens)
	}
	for _, key := range []string{"temperature", "top_p", "top_k"} {
		if _, ok := got[key]; ok {
			t.Errorf("%s must not be sent to Claude", key)
		}
	}
	if oc, ok := got["output_config"].(map[string]any); !ok || oc["effort"] != "low" {
		t.Errorf("output_config = %v, want effort low", got["output_config"])
	}
}

func TestAnthropicParamsKeepsMaxCompletionTokens(t *testing.T) {
	got := paramsJSON(t, openai.ChatCompletionRequest{
		MaxCompletionTokens: 512,
		Messages:            []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	})
	if got["max_tokens"] != float64(512) {
		t.Errorf("max_tokens = %v, want 512", got["max_tokens"])
	}
}

func TestAnthropicParamsSkipsEmptyAndTrailingAssistantMessages(t *testing.T) {
	got := paramsJSON(t, openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "look at this"},
			{Role: openai.ChatMessageRoleAssistant, Content: ""},
			{Role: openai.ChatMessageRoleUser, Content: "  "},
			{Role: openai.ChatMessageRoleUser, Content: "what is it?"},
			{Role: openai.ChatMessageRoleAssistant, Content: "It is"},
		},
	})

	msgs := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (empties and trailing assistant dropped): %v", len(msgs), msgs)
	}
	for i, m := range msgs {
		if role := m.(map[string]any)["role"]; role != "user" {
			t.Errorf("messages[%d].role = %v, want user", i, role)
		}
	}
}

func TestAnthropicParamsConvertsDataURLImages(t *testing.T) {
	got := paramsJSON(t, openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{
			Role: openai.ChatMessageRoleUser,
			MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeText, Text: "what do you see"},
				{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/jpeg;base64,QUJD"}},
			},
		}},
	})

	content := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(content))
	}
	if content[0].(map[string]any)["text"] != "what do you see" {
		t.Errorf("text block = %v", content[0])
	}
	image := content[1].(map[string]any)
	source := image["source"].(map[string]any)
	if image["type"] != "image" || source["type"] != "base64" || source["media_type"] != "image/jpeg" || source["data"] != "QUJD" {
		t.Errorf("image block = %v", image)
	}
}

func TestAnthropicParamsConvertsRemoteImageURLs(t *testing.T) {
	got := paramsJSON(t, openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{
			Role: openai.ChatMessageRoleUser,
			MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "https://example.com/cat.jpg"}},
			},
		}},
	})

	source := got["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["source"].(map[string]any)
	if source["type"] != "url" || source["url"] != "https://example.com/cat.jpg" {
		t.Errorf("image source = %v", source)
	}
}

func sseEvent(name, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", name, data)
}

func TestAnthropicStreamYieldsTextDeltasThenEOF(t *testing.T) {
	var gotKey string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("content-type", "text/event-stream")
		io.WriteString(w, sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}`))
		io.WriteString(w, sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`))
		io.WriteString(w, sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`))
		io.WriteString(w, sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", friend!"}}`))
		io.WriteString(w, sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`))
		io.WriteString(w, sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`))
		io.WriteString(w, sseEvent("message_stop", `{"type":"message_stop"}`))
	}))
	defer server.Close()

	client := newAnthropicClient("test-key", option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	stream, err := newAnthropicStream(context.Background(), client, openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "You are Vector."},
			{Role: openai.ChatMessageRoleUser, Content: "Say hi"},
		},
	})
	if err != nil {
		t.Fatalf("newAnthropicStream: %v", err)
	}
	defer stream.Close()

	var text strings.Builder
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if len(resp.Choices) != 1 {
			t.Fatalf("choices = %d, want 1", len(resp.Choices))
		}
		text.WriteString(resp.Choices[0].Delta.Content)
	}

	if text.String() != "Hello, friend!" {
		t.Errorf("streamed text = %q, want %q", text.String(), "Hello, friend!")
	}
	if gotKey != "test-key" {
		t.Errorf("x-api-key = %q, want test-key", gotKey)
	}
	if gotBody["stream"] != true {
		t.Errorf("request stream = %v, want true", gotBody["stream"])
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("Recv after end = %v, want io.EOF", err)
	}
}

func TestAnthropicStreamReturnsAPIErrorOnOpen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer server.Close()

	client := newAnthropicClient("bad-key", option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	stream, err := newAnthropicStream(context.Background(), client, openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	})
	if err == nil {
		stream.Close()
		t.Fatal("newAnthropicStream error = nil, want authentication error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want it to mention 401", err)
	}
}

func TestAnthropicParamsDisablesThinkingForFastReplies(t *testing.T) {
	for _, model := range []string{"", "claude-haiku-5-5", "claude-sonnet-5-5"} {
		got := paramsJSON(t, openai.ChatCompletionRequest{
			Model:    model,
			Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
		})
		thinking, ok := got["thinking"].(map[string]any)
		if !ok || thinking["type"] != "disabled" {
			t.Errorf("model %q: thinking = %v, want disabled", model, got["thinking"])
		}
	}
}

func TestAnthropicParamsLeavesThinkingUnsetWhereItCannotBeDisabled(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-fable-5-1"} {
		got := paramsJSON(t, openai.ChatCompletionRequest{
			Model:    model,
			Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
		})
		if _, ok := got["thinking"]; ok {
			t.Errorf("model %q: thinking = %v, want it omitted", model, got["thinking"])
		}
	}
}

func TestSharedAnthropicClientIsReusedUntilTheKeyChanges(t *testing.T) {
	saved := vars.APIConfig.Knowledge.Key
	defer func() { vars.APIConfig.Knowledge.Key = saved }()

	vars.APIConfig.Knowledge.Key = "key-a"
	first := sharedAnthropicClient()
	if again := sharedAnthropicClient(); again != first {
		t.Error("client was rebuilt although the key did not change")
	}
	vars.APIConfig.Knowledge.Key = "key-b"
	if changed := sharedAnthropicClient(); changed == first {
		t.Error("client was reused after the key changed")
	}
}

func TestAnthropicClientRetriesOverloadedRequestsOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("content-type", "application/json")
		w.Header().Set("retry-after-ms", "1")
		w.WriteHeader(529)
		io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	}))
	defer server.Close()

	client := newAnthropicClient("test-key", option.WithBaseURL(server.URL))
	_, err := newAnthropicStream(context.Background(), client, openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("error = nil, want overloaded error")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (one retry)", got)
	}
}
