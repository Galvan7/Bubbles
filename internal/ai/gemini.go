package ai

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

const geminiFlashModel = "gemini-3.6-flash"

type Gemini struct {
	client    *genai.Client
	modelName string
}

func (c *Config) NewGemini(ctx context.Context) (*Gemini, error) {
	if c.Provider != ProviderGemini {
		return nil, fmt.Errorf("provider %q has no AI backend implemented yet (supported: gemini)", c.Provider)
	}
	key := os.Getenv(c.KeyEnv())
	if key == "" {
		return nil, fmt.Errorf("missing %s (see .env.example)", c.KeyEnv())
	}
	client, err := genai.NewClient(ctx, option.WithAPIKey(key))
	if err != nil {
		return nil, fmt.Errorf("creating gemini client: %v", err)
	}
	return &Gemini{client: client, modelName: geminiFlashModel}, nil
}

func (g *Gemini) Close() error {
	return g.client.Close()
}

func (g *Gemini) GenerateJSON(ctx context.Context, instruction, content string) (string, error) {
	model := g.client.GenerativeModel(g.modelName)
	model.ResponseMIMEType = "application/json"
	temp := float32(0.1)
	model.Temperature = &temp
	maxOut := int32(8192)
	model.MaxOutputTokens = &maxOut

	resp, err := model.GenerateContent(ctx, genai.Text(instruction), genai.Text(content))
	if err != nil {
		return "", fmt.Errorf("gemini generation failed: %v", err)
	}
	if len(resp.Candidates) == 0 {
		reason := "unknown"
		if resp.PromptFeedback != nil {
			reason = resp.PromptFeedback.BlockReason.String()
		}
		return "", fmt.Errorf("gemini produced no candidates (block reason: %s)", reason)
	}
	candidate := resp.Candidates[0]
	if len(candidate.Content.Parts) == 0 {
		return "", fmt.Errorf("gemini candidate had no content parts")
	}

	var sb strings.Builder
	for _, part := range candidate.Content.Parts {
		if text, ok := part.(genai.Text); ok {
			sb.WriteString(string(text))
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("gemini candidate had no text content")
	}
	return strings.TrimSpace(sb.String()), nil
}
