package wirepodbrain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
	ttr "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/ttr"
	"github.com/sashabaranov/go-openai"
)

const (
	defaultAnthropicModel = "claude-haiku-5-5" // wire-pod's default for spoken replies
	maxAnswerTokens       = 1024
)

// LLM answers with the provider, model, prompt and remembered chat configured in wire-pod's web interface
// (Knowledge settings), as one complete reply instead of the stream wire-pod speaks sentence by sentence.
// Persona, when set, replaces the robot's name "Vector" in the system prompt.
type LLM struct {
	Persona string
	// WithoutRunIntent leaves runIntent out of the prompt; for the intent audit's latency comparison only.
	WithoutRunIntent bool

	mu     sync.Mutex
	key    string
	client *anthropic.Client
}

// Answer implements Config.LLM: lang ("es", "en") is the language to answer in. It returns ErrNoLLM when the
// LLM is off in wire-pod's settings.
func (l *LLM) Answer(ctx context.Context, esn, text, lang string) (string, error) {
	k := vars.APIConfig.Knowledge
	if !k.Enable {
		return "", ErrNoLLM
	}
	req := l.request(esn, text, lang)
	var answer string
	var err error
	switch k.Provider {
	case "anthropic":
		answer, err = l.anthropic(ctx, req)
	case "openai", "together", "custom":
		answer, err = openaiAnswer(ctx, req)
	default:
		return "", fmt.Errorf("unsupported LLM provider %q", k.Provider)
	}
	if err != nil {
		return "", err
	}
	if k.SaveChat {
		ttr.Remember(openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: text},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: answer}, esn)
	}
	return answer, nil
}

// replyInstructions are added to the system prompt so the LLM answers in the question's language (L101):
// wire-pod's default prompt is English, so a Spanish question got an English answer.
var replyInstructions = map[string]string{"es": "Responde siempre en español.", "en": "Answer in English."}

// request is wire-pod's LLM request for text, with the persona's name, the runIntent command (when wire-pod's LLM
// commands are on) and, for lang "es" or "en", the instruction to answer in it. wire-pod's own requests
// (ttr.CreateAIReq) are not changed.
func (l *LLM) request(esn, text, lang string) openai.ChatCompletionRequest {
	cmds := llmCommands
	if l.WithoutRunIntent {
		cmds = nil
	}
	req := ttr.CreateAIReqWith(text, esn, false, false, cmds)
	req.Stream = false
	req.MaxCompletionTokens = maxAnswerTokens
	l.rename(&req)
	if instr := replyInstructions[lang]; instr != "" {
		for i, m := range req.Messages {
			if m.Role == openai.ChatMessageRoleSystem {
				req.Messages[i].Content = strings.TrimSpace(m.Content) + "\n\n" + instr
				return req
			}
		}
		req.Messages = append([]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleSystem, Content: instr}},
			req.Messages...)
	}
	return req
}

// rename gives the robot its persona's name in the system prompt.
func (l *LLM) rename(req *openai.ChatCompletionRequest) {
	if l.Persona == "" {
		return
	}
	for i, m := range req.Messages {
		if m.Role == openai.ChatMessageRoleSystem {
			req.Messages[i].Content = strings.ReplaceAll(m.Content, "Vector", l.Persona)
		}
	}
}

func (l *LLM) anthropicClient() *anthropic.Client {
	key := strings.TrimSpace(vars.APIConfig.Knowledge.Key)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client == nil || key != l.key {
		opts := []option.RequestOption{option.WithMaxRetries(1)}
		if key != "" {
			opts = append(opts, option.WithAPIKey(key))
		}
		c := anthropic.NewClient(opts...)
		l.client, l.key = &c, key
	}
	return l.client
}

func (l *LLM) anthropic(ctx context.Context, req openai.ChatCompletionRequest) (string, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = defaultAnthropicModel
	}
	var system []string
	var msgs []anthropic.MessageParam
	for _, m := range req.Messages {
		text := strings.TrimSpace(m.Content)
		switch {
		case text == "":
		case m.Role == openai.ChatMessageRoleSystem:
			system = append(system, text)
		case m.Role == openai.ChatMessageRoleAssistant:
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(text)))
		default:
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(text)))
		}
	}
	params := anthropic.MessageNewParams{Model: anthropic.Model(model), MaxTokens: maxAnswerTokens, Messages: msgs}
	// A spoken answer needs no thinking; Haiku answers faster without it.
	if strings.HasPrefix(model, "claude-haiku-") {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
	}
	if len(system) > 0 {
		params.System = []anthropic.TextBlockParam{{Text: strings.Join(system, "\n\n")}}
	}
	msg, err := l.anthropicClient().Messages.New(ctx, params)
	if err != nil {
		return "", err
	}
	var out []string
	for _, block := range msg.Content {
		if block.Type == "text" {
			out = append(out, block.Text)
		}
	}
	return strings.Join(out, ""), nil
}

func openaiAnswer(ctx context.Context, req openai.ChatCompletionRequest) (string, error) {
	k := vars.APIConfig.Knowledge
	conf := openai.DefaultConfig(k.Key)
	switch k.Provider {
	case "together":
		conf.BaseURL = "https://api.together.xyz/v1"
	case "custom":
		conf.BaseURL = k.Endpoint
	}
	resp, err := openai.NewClientWithConfig(conf).CreateChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("the LLM returned no answer")
	}
	return resp.Choices[0].Message.Content, nil
}
