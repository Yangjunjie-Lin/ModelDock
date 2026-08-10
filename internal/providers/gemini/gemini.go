package gemini

import (
	"net/http"

	"github.com/relayedock/relayedock/internal/providers/openai"
)

// Provider targets Gemini's official OpenAI-compatible endpoint.
type Provider struct{ *openai.Adapter }

func New(client *http.Client) *Provider { return &Provider{Adapter: openai.New(client)} }
