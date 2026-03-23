package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
	"github.com/meganerd/electrictown/internal/agent/transport"
	"github.com/meganerd/electrictown/internal/provider"
)

// ollamaTagsResponse is the JSON payload from GET /api/tags.
type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// openaiModelsResponse is the JSON payload from GET /v1/models (OpenAI-compatible).
// Used by LM Studio and other OpenAI-compatible providers.
type openaiModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// cmdNodes implements "et nodes": pings each Ollama provider and lists models.
func cmdNodes(args []string) error {
	fs := flag.NewFlagSet("nodes", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: ./electrictown.yaml, then $HOME/electrictown.yaml)")
	discover := fs.Bool("discover", false, "discover coding agent CLIs on SSH-configured agent hosts")
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

	// Show LM Studio instances.
	for name, pc := range cfg.Providers {
		if pc.Type != "lmstudio" {
			continue
		}
		baseURL := pc.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:1234"
		}

		modelsURL := baseURL + "/v1/models"
		resp, err := client.Get(modelsURL)
		if err != nil {
			fmt.Printf("%-20s %-40s ✗ offline (%v)\n", name, baseURL, trimErr(err))
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("%-20s %-40s ✗ HTTP %d\n", name, baseURL, resp.StatusCode)
			continue
		}

		var models openaiModelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
			fmt.Printf("%-20s %-40s ✗ parse error: %v\n", name, baseURL, err)
			continue
		}

		if len(models.Data) == 0 {
			fmt.Printf("%-20s %-40s ✓ online (no models loaded)\n", name, baseURL)
			continue
		}

		fmt.Printf("%-20s %-40s ✓ %s\n", name, baseURL, models.Data[0].ID)
		for _, m := range models.Data[1:] {
			fmt.Printf("%-20s %-40s   %s\n", "", "", m.ID)
		}
	}

	// Show agent status if any agents are configured.
	if len(cfg.Agents) > 0 {
		fmt.Printf("\n%-20s %-40s %s\n", "AGENT", "TYPE / TRANSPORT", "STATUS")
		fmt.Printf("%-20s %-40s %s\n", "-----", "----------------", "------")

		for name, ac := range cfg.Agents {
			command := ac.Command
			if command == "" {
				command = agent.DefaultCommand(ac.Type)
			}
			typeInfo := fmt.Sprintf("%s / %s", ac.Type, ac.Transport)
			if ac.Transport == "" {
				typeInfo = fmt.Sprintf("%s / local", ac.Type)
			}
			if ac.Transport == "ssh" {
				typeInfo = fmt.Sprintf("%s / ssh:%s", ac.Type, ac.Host)
			}

			if ac.Transport == "ssh" {
				if !*discover {
					fmt.Printf("%-20s %-40s ? ssh (use --discover to check)\n", name, typeInfo)
					continue
				}
				// SSH discovery: connect and check for binary.
				mgr := transport.NewSSHManager()
				authCfg := transport.AuthConfig{
					User:    ac.SSHUser,
					Port:    ac.SSHPort,
					KeyPath: ac.SSHKey,
				}
				client, err := mgr.GetConnection(ac.Host, ac.SSHPort, authCfg)
				if err != nil {
					fmt.Printf("%-20s %-40s ✗ ssh connect failed: %s\n", name, typeInfo, trimErr(err))
					mgr.Close()
					continue
				}
				agents, err := transport.DiscoverAgents(client, mgr)
				mgr.Close()
				if err != nil {
					fmt.Printf("%-20s %-40s ✗ discovery failed: %s\n", name, typeInfo, trimErr(err))
					continue
				}
				// Find the matching agent type.
				found := false
				for _, da := range agents {
					if da.Type == ac.Type {
						if da.Found {
							fmt.Printf("%-20s %-40s ✓ %s\n", name, typeInfo, da.Version)
						} else {
							fmt.Printf("%-20s %-40s ✗ binary not found on remote\n", name, typeInfo)
						}
						found = true
						break
					}
				}
				if !found {
					fmt.Printf("%-20s %-40s ? unknown agent type\n", name, typeInfo)
				}
				continue
			}

			status := agent.CheckLocal(name, command)
			if status.Available {
				fmt.Printf("%-20s %-40s ✓ %s\n", name, typeInfo, status.Detail)
			} else {
				fmt.Printf("%-20s %-40s ✗ %s\n", name, typeInfo, status.Detail)
			}
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
