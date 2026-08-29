package ai

import (
	"fmt"
	"os"
)

const apiKeyEnv = "GEMINI_API_KEY"

type Config struct{}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) RequireKey() error {
	if os.Getenv(apiKeyEnv) == "" {
		return fmt.Errorf("missing %s — set it via: export %s=<your key>", apiKeyEnv, apiKeyEnv)
	}
	return nil
}
