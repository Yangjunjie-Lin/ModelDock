package cockpit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	maxSnapshotBytes = 1 << 20
	testReply        = "RELAYDOCK_COCKPIT_OK"
)

var ErrTestNotConfigured = errors.New("cockpit sidecar testing is not configured")

type Config struct {
	SnapshotPath string
	BaseURL      string
	APIKey       string
	TestModel    string
	HTTPClient   *http.Client
}

type Client struct {
	snapshotPath string
	baseURL      string
	apiKey       string
	testModel    string
	httpClient   *http.Client
}

type Account struct {
	ID                    string     `json:"id"`
	EmailMasked           string     `json:"email_masked"`
	Plan                  string     `json:"plan"`
	AuthKind              string     `json:"auth_kind"`
	Status                string     `json:"status"`
	RemainingQuota        int        `json:"remaining_quota"`
	RemainingPercent      int        `json:"remaining_percent"`
	SecondaryPercent      int        `json:"secondary_percent"`
	ResetAt               *time.Time `json:"reset_at,omitempty"`
	SubscriptionExpiresAt *time.Time `json:"subscription_expires_at,omitempty"`
	UpdatedAt             *time.Time `json:"updated_at,omitempty"`
}

type TestResult struct {
	OK        bool      `json:"ok"`
	Model     string    `json:"model"`
	LatencyMS int64     `json:"latency_ms"`
	TestedAt  time.Time `json:"tested_at"`
	Message   string    `json:"message"`
}

type Pool struct {
	Configured     bool        `json:"configured"`
	TestConfigured bool        `json:"test_configured"`
	Source         string      `json:"source"`
	GeneratedAt    *time.Time  `json:"generated_at,omitempty"`
	Accounts       []Account   `json:"accounts"`
	LastTest       *TestResult `json:"last_test,omitempty"`
}

type snapshot struct {
	Source      string      `json:"source"`
	GeneratedAt *time.Time  `json:"generated_at"`
	Accounts    []Account   `json:"accounts"`
	LastTest    *TestResult `json:"last_test"`
}

func New(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}
	return &Client{
		snapshotPath: strings.TrimSpace(cfg.SnapshotPath),
		baseURL:      strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		apiKey:       strings.TrimSpace(cfg.APIKey),
		testModel:    strings.TrimSpace(cfg.TestModel),
		httpClient:   httpClient,
	}
}

// Pool reads only the sanitized snapshot produced by scripts/sync-cockpit.ps1.
// RelayDock never reads Cockpit OAuth, cookie, or upstream-account files.
func (c *Client) Pool() (Pool, error) {
	result := Pool{
		Configured:     false,
		TestConfigured: c.baseURL != "" && c.apiKey != "",
		Source:         "cockpit-local-sidecar",
		Accounts:       []Account{},
	}
	if c.snapshotPath == "" {
		return result, nil
	}
	f, err := os.Open(c.snapshotPath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("open Cockpit snapshot: %w", err)
	}
	defer f.Close()

	var value snapshot
	decoder := json.NewDecoder(io.LimitReader(f, maxSnapshotBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return result, fmt.Errorf("decode Cockpit snapshot: %w", err)
	}
	if value.Accounts == nil {
		value.Accounts = []Account{}
	}
	result.Configured = true
	result.Source = value.Source
	result.GeneratedAt = value.GeneratedAt
	result.Accounts = value.Accounts
	result.LastTest = value.LastTest
	return result, nil
}

func (c *Client) Test(ctx context.Context) (TestResult, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return TestResult{}, ErrTestNotConfigured
	}
	parsed, err := url.Parse(c.baseURL)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return TestResult{}, errors.New("invalid Cockpit sidecar URL")
	}
	model := c.testModel
	if model == "" {
		model = "gpt-5.6-luna"
	}
	body, err := json.Marshal(map[string]any{
		"model":             model,
		"input":             "Reply exactly: " + testReply,
		"max_output_tokens": 32,
	})
	if err != nil {
		return TestResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return TestResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	started := time.Now()
	response, err := c.httpClient.Do(req)
	if err != nil {
		return TestResult{}, fmt.Errorf("Cockpit sidecar request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return TestResult{}, fmt.Errorf("Cockpit sidecar returned HTTP %d", response.StatusCode)
	}
	var out struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&out); err != nil {
		return TestResult{}, errors.New("Cockpit sidecar returned an invalid response")
	}
	text := out.OutputText
	if text == "" {
		var parts []string
		for _, item := range out.Output {
			for _, content := range item.Content {
				parts = append(parts, content.Text)
			}
		}
		text = strings.Join(parts, "")
	}
	ok := strings.TrimSpace(text) == testReply
	result := TestResult{
		OK:        ok,
		Model:     model,
		LatencyMS: time.Since(started).Milliseconds(),
		TestedAt:  time.Now().UTC(),
		Message:   "Cockpit sidecar model check passed",
	}
	if !ok {
		result.Message = "Cockpit sidecar responded, but the verification text did not match"
		return result, errors.New(result.Message)
	}
	return result, nil
}
