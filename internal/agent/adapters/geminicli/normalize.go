package geminicli

import (
	"encoding/json"

	"github.com/meganerd/electrictown/internal/agent"
)

// geminiResult represents the JSON output from Gemini CLI --output-format json.
type geminiResult struct {
	Response string `json:"response"`
	Result   string `json:"result"`
	Error    string `json:"error"`
}

// GeminiCLINormalizer parses Gemini CLI JSON output into structured results.
type GeminiCLINormalizer struct{}

// Normalize parses Gemini CLI's JSON output and enriches the Result.
func (n *GeminiCLINormalizer) Normalize(result *agent.Result) error {
	if result.Stdout == "" {
		return nil
	}

	var gr geminiResult
	if err := json.Unmarshal([]byte(result.Stdout), &gr); err != nil {
		// Not JSON — leave stdout as-is (text mode fallback).
		return nil
	}

	// Gemini CLI may use "response" or "result" field.
	text := gr.Response
	if text == "" {
		text = gr.Result
	}
	if text != "" {
		result.Stdout = text
	}

	if gr.Error != "" {
		result.Stderr = gr.Error
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
	}

	// Parse for diff-style file changes.
	diffNorm := &agent.DiffNormalizer{}
	return diffNorm.Normalize(result)
}

func init() {
	agent.RegisterNormalizer(agent.TypeGeminiCLI, &GeminiCLINormalizer{})
}
