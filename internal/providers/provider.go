package providers

import (
	"context"
	"io"
	"net/http"
)

type Credential struct {
	Secret         string
	OrganizationID string
	ProjectID      string
}
type ForwardRequest struct {
	BaseURL         string
	Path            string
	Body            io.Reader
	ContentType     string
	Accept          string
	ClientRequestID string
	Credential      Credential
}
type Provider interface {
	Forward(context.Context, ForwardRequest) (*http.Response, error)
	ListModels(context.Context, string, Credential) ([]Model, error)
	CreateResponse(context.Context, ForwardRequest) (*http.Response, error)
	CreateChatCompletion(context.Context, ForwardRequest) (*http.Response, error)
	CreateEmbedding(context.Context, ForwardRequest) (*http.Response, error)
	HealthCheck(context.Context, string, Credential) error
}
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}
