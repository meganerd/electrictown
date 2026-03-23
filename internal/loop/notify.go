package loop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// notifyEndpoint is the PAI voice notification endpoint.
const notifyEndpoint = "http://localhost:8888/notify"

// notifyPayload is the JSON body for the notification endpoint.
type notifyPayload struct {
	Message      string `json:"message"`
	VoiceID      string `json:"voice_id,omitempty"`
	VoiceEnabled bool   `json:"voice_enabled"`
}

// notify sends a voice notification via the PAI notification endpoint.
// Failures are logged but not fatal — notifications are best-effort.
func notify(message string) {
	payload := notifyPayload{
		Message:      message,
		VoiceID:      "a1TnjruAs5jTzdrjL8Vd",
		VoiceEnabled: true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(notifyEndpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	resp.Body.Close()
}

// FormatReport generates a summary report string from loop results.
func FormatReport(result *Result) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "═══ et loop Summary ═══\n")
	fmt.Fprintf(&b, "Duration:    %s\n", result.Duration.Round(time.Second))
	fmt.Fprintf(&b, "Iterations:  %d\n", result.Iterations)
	fmt.Fprintf(&b, "Completed:   %d\n", len(result.Completed))
	fmt.Fprintf(&b, "Failed:      %d\n", len(result.Failed))
	fmt.Fprintf(&b, "Skipped:     %d\n", len(result.Skipped))

	if len(result.Completed) > 0 {
		fmt.Fprintf(&b, "\n✓ Completed tickets:\n")
		for _, id := range result.Completed {
			fmt.Fprintf(&b, "  %s\n", id)
		}
	}

	if len(result.Failed) > 0 {
		fmt.Fprintf(&b, "\n✗ Failed tickets (max retries exhausted):\n")
		for _, id := range result.Failed {
			fmt.Fprintf(&b, "  %s\n", id)
		}
	}

	return b.String()
}
