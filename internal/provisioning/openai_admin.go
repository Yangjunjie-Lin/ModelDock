package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const openAIAdminDocs = "https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/methods/create"

// OpenAIAdmin provisions official API Platform projects and project service
// accounts. OpenAI's documented Admin API does not expose project credit
// transfer, so SupportsAutomaticCredit remains false.
type OpenAIAdmin struct {
	client  *http.Client
	baseURL string
	apiKey  string
	enabled bool
}

func NewOpenAIAdmin(client *http.Client, apiKey string, enabled bool) *OpenAIAdmin {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OpenAIAdmin{client: client, baseURL: "https://api.openai.com/v1", apiKey: strings.TrimSpace(apiKey), enabled: enabled && strings.TrimSpace(apiKey) != ""}
}

func newOpenAIAdminWithBaseURL(client *http.Client, baseURL, apiKey string, enabled bool) *OpenAIAdmin {
	adapter := NewOpenAIAdmin(client, apiKey, enabled)
	adapter.baseURL = strings.TrimRight(baseURL, "/")
	return adapter
}

func (adapter *OpenAIAdmin) Capabilities() Capability {
	reason := "Set RELAYDOCK_OPENAI_PROVISIONING_ENABLED=true and OPENAI_ADMIN_KEY to enable official project/service-account provisioning. Project credit transfer is not documented by OpenAI."
	if adapter.enabled {
		reason = "Official project and service-account creation is enabled. Billing remains organization-level; no project credit-transfer API is claimed."
	}
	return Capability{ProviderType: "openai", Mode: "OFFICIAL_ENTERPRISE", Enabled: adapter.enabled,
		SupportsAutomaticBinding: true, SupportsAutomaticCredit: false, SupportsRefresh: true,
		Reason: reason, DocumentationURL: openAIAdminDocs}
}

func (adapter *OpenAIAdmin) EnsureBinding(ctx context.Context, request BindingRequest) (BindingResult, error) {
	if !adapter.enabled {
		return BindingResult{}, ErrAutomaticUnsupported
	}
	projectID := strings.TrimSpace(request.ExternalProjectID)
	name := stableOpenAIName(request.BindingID)
	if projectID == "" {
		var project struct {
			ID string `json:"id"`
		}
		if err := adapter.do(ctx, http.MethodPost, "/organization/projects", request.IdempotencyKey+":project", map[string]any{"name": name}, &project); err != nil {
			return BindingResult{}, err
		}
		if project.ID == "" {
			return BindingResult{}, errors.New("OpenAI project response did not contain an id")
		}
		projectID = project.ID
	}
	var service struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		APIKey struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		} `json:"api_key"`
	}
	path := "/organization/projects/" + projectID + "/service_accounts"
	if err := adapter.do(ctx, http.MethodPost, path, request.IdempotencyKey+":service-account", map[string]any{"name": name + " service"}, &service); err != nil {
		return BindingResult{}, err
	}
	if service.ID == "" || service.APIKey.Value == "" {
		return BindingResult{}, errors.New("OpenAI service-account response did not contain the one-time API key")
	}
	return BindingResult{ExternalAccountID: service.ID, ExternalProjectID: projectID, CredentialSecret: service.APIKey.Value,
		CredentialType: "api_key", CredentialName: service.Name,
		Metadata: map[string]any{"api_key_id": service.APIKey.ID, "source": "openai_admin_api"}}, nil
}

func (adapter *OpenAIAdmin) AllocateCredit(context.Context, AllocationRequest) (AllocationResult, error) {
	return AllocationResult{}, ErrAutomaticUnsupported
}

func (adapter *OpenAIAdmin) RefreshBinding(ctx context.Context, request BindingRequest) (BindingResult, error) {
	if !adapter.enabled || strings.TrimSpace(request.ExternalProjectID) == "" {
		return BindingResult{}, ErrAutomaticUnsupported
	}
	var project struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := adapter.do(ctx, http.MethodGet, "/organization/projects/"+request.ExternalProjectID, request.IdempotencyKey, nil, &project); err != nil {
		return BindingResult{}, err
	}
	return BindingResult{ExternalAccountID: request.ExternalAccountID, ExternalProjectID: project.ID,
		Metadata: map[string]any{"project_name": project.Name, "project_status": project.Status, "source": "openai_admin_api"}}, nil
}

func (adapter *OpenAIAdmin) do(ctx context.Context, method, path, idempotencyKey string, body any, output any) error {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, adapter.baseURL+path, payload)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+adapter.apiKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return fmt.Errorf("OpenAI Admin API request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("OpenAI Admin API returned HTTP %d", response.StatusCode)
	}
	if output == nil {
		_, err = io.Copy(io.Discard, response.Body)
		return err
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode OpenAI Admin API response: %w", err)
	}
	return nil
}

func stableOpenAIName(bindingID string) string {
	compact := strings.ReplaceAll(strings.TrimSpace(bindingID), "-", "")
	if len(compact) > 20 {
		compact = compact[:20]
	}
	return "relayedock-" + compact
}
