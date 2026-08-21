package openai

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestEndpointDialerRejectsPrivateResolvedAddress(t *testing.T) {
	policy := EndpointPolicy{AllowedHosts: []string{"localhost"}, AllowHTTP: true}
	_, err := policy.dialContext(context.Background(), "tcp", "localhost:80")
	if err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("private endpoint error=%v", err)
	}
}

func TestEndpointClientRejectsRedirects(t *testing.T) {
	adapter := NewWithPolicy(nil, EndpointPolicy{AllowedHosts: []string{"api.example.invalid"}})
	request, _ := http.NewRequest(http.MethodGet, "https://api.example.invalid/v1", nil)
	if err := adapter.client.CheckRedirect(request, nil); err == nil {
		t.Fatal("provider redirect was not rejected")
	}
}
