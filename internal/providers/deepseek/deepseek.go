package deepseek

import (
	"net/http"

	"github.com/relayedock/relayedock/internal/providers/openai"
)

// Provider targets DeepSeek's official OpenAI-compatible endpoint.
type Provider struct{ *openai.Adapter }

func New(client *http.Client) *Provider { return &Provider{Adapter: openai.New(client)} }

func NewWithPolicy(client *http.Client, policy openai.EndpointPolicy) *Provider {
	return &Provider{Adapter: openai.NewWithPolicy(client, policy)}
}
