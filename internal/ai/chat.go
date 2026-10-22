package ai

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// DefaultGrammarPrompt is the system prompt for ImproveGrammar.
const DefaultGrammarPrompt = `You are a meticulous proofreader. Correct the spelling, grammar, punctuation and obvious typos in the text the user sends.
Rules:
- Keep the original meaning, tone, wording and language. Do not translate, rephrase for style, shorten or expand it.
- Keep the formatting exactly: line breaks, blank lines, lists, greetings and signatures.
- If the text contains HTML, keep every tag and attribute exactly as it is and only correct the visible text.
- Reply with the corrected text only: no explanations, comments, quotes or code fences.
- If nothing needs fixing, return the text unchanged.`

const summaryPrompt = `Summarize the email for a busy reader in 2 to 4 short bullet points, each starting with "- ".
Write in the same language as the email. Mention action items, deadlines and amounts if present. No preamble, no closing remarks.`

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat runs a chat completion and returns the assistant text. effort is
// the reasoning effort for reasoning models ("" = the model's default);
// OpenRouter maps it per provider, and models without reasoning ignore it.
func (c *Client) Chat(ctx context.Context, model, system, user string, temperature float64, effort string) (string, error) {
	api, err := c.api(true)
	if err != nil {
		return "", err
	}
	if model == "" {
		return "", errors.New("choose an AI model in Settings")
	}
	if IsDecisionModel(model) {
		return "", errors.New("the Jev decision model can't write text; choose a chat model for writing features in Settings")
	}
	req := map[string]any{
		"model":       model,
		"messages":    []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		"temperature": temperature,
		"usage":       map[string]bool{"include": true}, // ask OpenRouter to report the cost
	}
	if effort != "" {
		// Only the answer is used: don't send the reasoning back.
		req["reasoning"] = map[string]any{"effort": effort, "exclude": true}
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage struct {
			PromptTokens     int     `json:"prompt_tokens"`
			CompletionTokens int     `json:"completion_tokens"`
			Cost             float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := api.JSONRetry(ctx, http.MethodPost, "v1/chat/completions", req, &resp); err != nil {
		return "", friendly(err)
	}
	c.report(Usage{Kind: "chat", Model: model, Cost: resp.Usage.Cost, InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens})
	if resp.Error != nil && resp.Error.Message != "" {
		return "", errors.New("OpenRouter: " + resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("the AI model returned no answer; please try again")
	}
	return resp.Choices[0].Message.Content, nil
}

var fenceRe = regexp.MustCompile("(?s)^\\s*```[a-zA-Z0-9_-]*\\s*\\n(.*?)\\n?```\\s*$")

// cleanRewrite strips wrappers some models add around rewritten text
// (code fences, surrounding quotes) when the input had none, and keeps the
// input's leading/trailing whitespace.
func cleanRewrite(input, output string) string {
	out := output
	if m := fenceRe.FindStringSubmatch(out); m != nil && !strings.Contains(input, "```") {
		out = m[1]
	}
	trimmedIn := strings.TrimSpace(input)
	t := strings.TrimSpace(out)
	for _, q := range [][2]string{{`"`, `"`}, {"“", "”"}, {"'", "'"}} {
		if strings.HasPrefix(t, q[0]) && strings.HasSuffix(t, q[1]) && len(t) >= len(q[0])+len(q[1]) &&
			!(strings.HasPrefix(trimmedIn, q[0]) && strings.HasSuffix(trimmedIn, q[1])) {
			t = t[len(q[0]) : len(t)-len(q[1])]
			break
		}
	}
	lead := input[:len(input)-len(strings.TrimLeft(input, " \t\r\n"))]
	trail := input[len(strings.TrimRight(input, " \t\r\n")):]
	return lead + strings.TrimSpace(t) + trail
}

// ImproveGrammar returns text with spelling and grammar corrected.
func (c *Client) ImproveGrammar(ctx context.Context, model, effort, systemPrompt, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = DefaultGrammarPrompt
	}
	out, err := c.Chat(ctx, model, systemPrompt, text, 0, effort)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "", errors.New("the AI model returned an empty answer; please try again")
	}
	return cleanRewrite(text, out), nil
}

// Summarize returns a short bullet summary of an email.
func (c *Client) Summarize(ctx context.Context, model, effort, email string) (string, error) {
	out, err := c.Chat(ctx, model, summaryPrompt, email, 0.2, effort)
	return strings.TrimSpace(out), err
}
