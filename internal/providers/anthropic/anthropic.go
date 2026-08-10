package anthropic

import (
	"net/http"

	"github.com/relayedock/relayedock/internal/providers/openai"
)

// Provider uses Anthropic's OpenAI SDK compatibility surface while retaining
// a distinct runtime type for provider-specific evolution.
type Provider struct{ *openai.Adapter }

func New(client *http.Client) *Provider { return &Provider{Adapter: openai.New(client)} }
