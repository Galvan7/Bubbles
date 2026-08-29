package ai

import (
	"fmt"
	"os"
	"strings"
)

type Provider string

const (
	ProviderGemini Provider = "gemini"
	ProviderGroq   Provider = "groq"
)

type Config struct {
	Provider Provider
}

func NewConfig(s string) (*Config, error) {
	p := Provider(strings.ToLower(strings.TrimSpace(s)))
	switch p {
	case ProviderGemini, ProviderGroq:
	default:
		return nil, fmt.Errorf("unsupported provider %q (supported: gemini, groq)", s)
	}
	return &Config{Provider: p}, nil
}

func (c *Config) KeyEnv() string {
	if c.Provider == ProviderGroq {
		return "GROQ_API_KEY"
	}
	return "GEMINI_API_KEY"
}

func (c *Config) RequireKey() error {
	if os.Getenv(c.KeyEnv()) == "" {
		return fmt.Errorf("missing %s — set it via: export %s=<your key>", c.KeyEnv(), c.KeyEnv())
	}
	return nil
}
