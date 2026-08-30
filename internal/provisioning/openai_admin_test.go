package provisioning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestOpenAIAdminCreatesOfficialProjectAndServiceAccount(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	paths := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Errorf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Idempotency-Key") == "" {
			t.Error("missing idempotency key")
		}
		mu.Lock()
		paths = append(paths, request.URL.Path)
		mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/organization/projects":
			_ = json.NewEncoder(response).Encode(map[string]any{"id": "proj_test"})
		case "/organization/projects/proj_test/service_accounts":
			_ = json.NewEncoder(response).Encode(map[string]any{"id": "svc_test", "name": "RelayDock service",
				"api_key": map[string]any{"id": "key_test", "value": "sk-test-secret"}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	adapter := newOpenAIAdminWithBaseURL(server.Client(), server.URL, "admin-secret", true)
	capability := adapter.Capabilities()
	if !capability.Enabled || !capability.SupportsAutomaticBinding || capability.SupportsAutomaticCredit {
		t.Fatalf("capability=%+v", capability)
	}
	result, err := adapter.EnsureBinding(context.Background(), BindingRequest{BindingID: "binding-openai",
		OrganizationID: "organization", UserID: "user", ProviderID: "provider", IdempotencyKey: "ensure-openai"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalProjectID != "proj_test" || result.ExternalAccountID != "svc_test" || result.CredentialSecret != "sk-test-secret" {
		t.Fatalf("result=%+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/organization/projects" || paths[1] != "/organization/projects/proj_test/service_accounts" {
		t.Fatalf("paths=%v", paths)
	}
}
