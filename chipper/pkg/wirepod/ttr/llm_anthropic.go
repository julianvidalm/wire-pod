package wirepod_ttr

import (
	"context"
	"io"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/kercre123/wire-pod/chipper/pkg/logger"
	"github.com/kercre123/wire-pod/chipper/pkg/vars"
	"github.com/sashabaranov/go-openai"
)

const (
	// defaultAnthropicModel is fast and cheap, which suits spoken replies.
	defaultAnthropicModel     = "claude-haiku-5-5"
	defaultAnthropicMaxTokens = 2048
)

// llmStream is the subset of *openai.ChatCompletionStream that the response
// loops use, so non-OpenAI providers can feed the same sentence splitter.
type llmStream interface {
	Recv() (openai.ChatCompletionStreamResponse, error)
	Close() error
}

// anthropicClientOptions uses the key from the web UI, or falls back to the
// ANTHROPIC_API_KEY environment variable when it is empty.
func anthropicClientOptions() []option.RequestOption {
	if key := strings.TrimSpace(vars.APIConfig.Knowledge.Key); key != "" {
		return []option.RequestOption{option.WithAPIKey(key)}
	}
	return nil
}

// anthropicParams converts an OpenAI-style chat request into a Claude Messages
// request. System messages become the cached system prompt, empty messages are
// skipped, and trailing assistant turns are dropped because current models
// reject assistant prefill. Sampling parameters are not sent: current models
// reject them.
func anthropicParams(req openai.ChatCompletionRequest) anthropic.MessageNewParams {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = defaultAnthropicModel
	}
	maxTokens := int64(req.MaxCompletionTokens)
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}

	var system []string
	var msgs []anthropic.MessageParam
	for _, m := range req.Messages {
		if m.Role == openai.ChatMessageRoleSystem {
			if text := strings.TrimSpace(m.Content); text != "" {
				system = append(system, text)
			}
			continue
		}
		blocks := anthropicContentBlocks(m)
		if len(blocks) == 0 {
			continue
		}
		if m.Role == openai.ChatMessageRoleAssistant {
			msgs = append(msgs, anthropic.NewAssistantMessage(blocks...))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(blocks...))
		}
	}
	for len(msgs) > 0 && msgs[len(msgs)-1].Role == anthropic.MessageParamRoleAssistant {
		msgs = msgs[:len(msgs)-1]
	}

	params := anthropic.MessageNewParams{
		Model:        anthropic.Model(model),
		MaxTokens:    maxTokens,
		Messages:     msgs,
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow},
	}
	if len(system) > 0 {
		params.System = []anthropic.TextBlockParam{{
			Text:         strings.Join(system, "\n\n"),
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}}
	}
	return params
}

func anthropicContentBlocks(m openai.ChatCompletionMessage) []anthropic.ContentBlockParamUnion {
	var blocks []anthropic.ContentBlockParamUnion
	if text := strings.TrimSpace(m.Content); text != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Content))
	}
	for _, part := range m.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				blocks = append(blocks, anthropic.NewTextBlock(part.Text))
			}
		case openai.ChatMessagePartTypeImageURL:
			if part.ImageURL != nil && part.ImageURL.URL != "" {
				blocks = append(blocks, anthropicImageBlock(part.ImageURL.URL))
			}
		}
	}
	return blocks
}

// anthropicImageBlock accepts data URLs (data:image/jpeg;base64,...) and
// remote image URLs.
func anthropicImageBlock(url string) anthropic.ContentBlockParamUnion {
	if rest, ok := strings.CutPrefix(url, "data:"); ok {
		if meta, data, ok := strings.Cut(rest, ","); ok {
			mediaType := strings.TrimSuffix(meta, ";base64")
			return anthropic.NewImageBlockBase64(mediaType, data)
		}
	}
	return anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: url})
}

type anthropicStream struct {
	stream  *ssestream.Stream[anthropic.MessageStreamEventUnion]
	primed  bool
	pending *anthropic.MessageStreamEventUnion
}

// newAnthropicStream starts a streaming Claude request. It reads the first
// event before returning so that HTTP and authentication errors surface here,
// like they do for go-openai's CreateChatCompletionStream.
func newAnthropicStream(ctx context.Context, req openai.ChatCompletionRequest, opts ...option.RequestOption) (llmStream, error) {
	client := anthropic.NewClient(opts...)
	params := anthropicParams(req)
	logger.Println("Using " + string(params.Model))
	stream := client.Messages.NewStreaming(ctx, params)
	s := &anthropicStream{stream: stream}
	if stream.Next() {
		event := stream.Current()
		s.pending = &event
	} else if err := stream.Err(); err != nil {
		stream.Close()
		return nil, err
	}
	return s, nil
}

func (s *anthropicStream) next() (anthropic.MessageStreamEventUnion, bool) {
	if s.pending != nil {
		event := *s.pending
		s.pending = nil
		return event, true
	}
	if !s.stream.Next() {
		return anthropic.MessageStreamEventUnion{}, false
	}
	return s.stream.Current(), true
}

// Recv returns the next text delta shaped like an OpenAI stream chunk, and
// io.EOF once the message is complete.
func (s *anthropicStream) Recv() (openai.ChatCompletionStreamResponse, error) {
	for {
		event, ok := s.next()
		if !ok {
			if err := s.stream.Err(); err != nil {
				return openai.ChatCompletionStreamResponse{}, err
			}
			return openai.ChatCompletionStreamResponse{}, io.EOF
		}
		switch ev := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			if delta, ok := ev.Delta.AsAny().(anthropic.TextDelta); ok && delta.Text != "" {
				return openai.ChatCompletionStreamResponse{
					Choices: []openai.ChatCompletionStreamChoice{{
						Delta: openai.ChatCompletionStreamChoiceDelta{
							Role:    openai.ChatMessageRoleAssistant,
							Content: delta.Text,
						},
					}},
				}, nil
			}
		case anthropic.MessageDeltaEvent:
			if ev.Delta.StopReason == anthropic.StopReasonRefusal {
				logger.Println("Claude declined to answer this request")
			}
		}
	}
}

func (s *anthropicStream) Close() error {
	return s.stream.Close()
}
