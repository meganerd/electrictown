package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/meganerd/electrictown/internal/provider"
)

// ollamaTagsResponse is the JSON payload from GET /api/tags.
type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// cmdNodes implements "et nodes": pings each Ollama provider and lists models.
func cmdNodes(args []string) error {
	fs := flag.NewFlagSet("nodes", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: ./electrictown.yaml, then $HOME/electrictown.yaml)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resolvedConfig, err := findConfig(*configPath)
	if err != nil {
		return err
	}

	cfg, err := provider.LoadConfig(resolvedConfig)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = ctx // ctx reserved for future use with request cancellation

	fmt.Printf("%-20s %-40s %s\n", "NODE", "URL", "STATUS / MODELS")
	fmt.Printf("%-20s %-40s %s\n", "----", "---", "---------------")

	for name, pc := range cfg.Providers {
		if pc.Type != "ollama" {
			continue
		}
		baseURL := pc.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}

		tagsURL := baseURL + "/api/tags"
		resp, err := client.Get(tagsURL)
		if err != nil {
			fmt.Printf("%-20s %-40s ✗ offline (%v)\n", name, baseURL, trimErr(err))
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("%-20s %-40s ✗ HTTP %d\n", name, baseURL, resp.StatusCode)
			continue
		}

		var tags ollamaTagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
			fmt.Printf("%-20s %-40s ✗ parse error: %v\n", name, baseURL, err)
			continue
		}

		if len(tags.Models) == 0 {
			fmt.Printf("%-20s %-40s ✓ online (no models)\n", name, baseURL)
			continue
		}

		// Print first model on the same line, remaining models indented.
		fmt.Printf("%-20s %-40s ✓ %s\n", name, baseURL, tags.Models[0].Name)
		for _, m := range tags.Models[1:] {
			fmt.Printf("%-20s %-40s   %s\n", "", "", m.Name)
		}
	}

	return nil
}

// trimErr shortens common connection error messages for table display.
func trimErr(err error) string {
	msg := err.Error()
	if len(msg) > 60 {
		return msg[:57] + "..."
	}
	return msg
}
