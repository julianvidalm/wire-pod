//go:build anthropic_live

package wirepod_ttr

// Live check against the real Claude API. It never runs in CI.
//   ANTHROPIC_API_KEY=... go test -tags "nolibopusfile anthropic_live" -run Live ./pkg/wirepod/ttr/

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"
)

func TestLiveAnthropicStream(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	stream, err := newAnthropicStream(ctx, openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "You are Vector, a small animated robot. Answer in one short sentence."},
			{Role: openai.ChatMessageRoleUser, Content: "Say hello to your owner."},
		},
	})
	if err != nil {
		t.Fatalf("newAnthropicStream: %v", err)
	}
	defer stream.Close()

	var text strings.Builder
	var firstChunk time.Duration
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if firstChunk == 0 {
			firstChunk = time.Since(start)
		}
		text.WriteString(resp.Choices[0].Delta.Content)
	}
	if strings.TrimSpace(text.String()) == "" {
		t.Fatal("empty response")
	}
	t.Logf("first chunk after %v, total %v: %q", firstChunk, time.Since(start), text.String())
}
