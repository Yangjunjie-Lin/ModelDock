// Package pricesync parses bounded Provider price feeds without converting
// monetary values to binary floating point.
package pricesync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/pricing"
)

const (
	MaxFeedBytes = 1 << 20
	MaxFeedRows  = 500
)

var ErrUnsafeSource = errors.New("pricing source is not an allowed public HTTPS endpoint")

type FetchResult struct {
	Changes         []domain.ProviderCostChangeRequest
	SourceReference string
}

type apiFeed struct {
	Prices []feedRow `json:"prices"`
}

type feedRow struct {
	ModelID              string  `json:"model_id"`
	InputTokenCost       string  `json:"input_token_cost"`
	CachedInputTokenCost string  `json:"cached_input_token_cost"`
	OutputTokenCost      string  `json:"output_token_cost"`
	RequestFixedCost     string  `json:"request_fixed_cost"`
	Currency             string  `json:"currency"`
	Unit                 int64   `json:"unit"`
	EffectiveAt          string  `json:"effective_at"`
	ExpiresAt            *string `json:"expires_at,omitempty"`
}

func FetchAPI(ctx context.Context, rawURL, providerID, batchKey string, allowedHosts []string) (FetchResult, error) {
	endpoint, host, addresses, err := validateSource(ctx, rawURL, allowedHosts)
	if err != nil {
		return FetchResult{}, err
	}
	dialer := net.Dialer{Timeout: 3 * time.Second, KeepAlive: -1}
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			requestedHost, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil || !strings.EqualFold(requestedHost, host) {
				return nil, ErrUnsafeSource
			}
			var lastErr error
			for _, address := range addresses {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 2 || request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), host) {
			return ErrUnsafeSource
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return FetchResult{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ModelDock-Provider-Pricing-Sync/1")
	response, err := client.Do(request)
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetch Provider pricing: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return FetchResult{}, fmt.Errorf("Provider pricing endpoint returned HTTP %d", response.StatusCode)
	}
	body, err := readBounded(response.Body)
	if err != nil {
		return FetchResult{}, err
	}
	changes, err := ParseAPI(body, providerID, batchKey)
	if err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Changes: changes, SourceReference: safeReference(endpoint, body)}, nil
}

func ParseAPI(body []byte, providerID, batchKey string) ([]domain.ProviderCostChangeRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var feed apiFeed
	if err := decoder.Decode(&feed); err != nil {
		return nil, fmt.Errorf("invalid Provider pricing response: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid Provider pricing response: trailing data")
	}
	if len(feed.Prices) == 0 || len(feed.Prices) > MaxFeedRows {
		return nil, fmt.Errorf("Provider pricing response must contain 1 to %d rows", MaxFeedRows)
	}
	changes := make([]domain.ProviderCostChangeRequest, len(feed.Prices))
	for index, row := range feed.Prices {
		change, err := row.change(providerID, "API", rowKey(batchKey, index), "")
		if err != nil {
			return nil, fmt.Errorf("price row %d: %w", index+1, err)
		}
		changes[index] = change
	}
	return changes, nil
}

func ParseCSV(body []byte, providerID, batchKey, sourceReference string) ([]domain.ProviderCostChangeRequest, error) {
	if len(body) == 0 || len(body) > MaxFeedBytes {
		return nil, fmt.Errorf("CSV must contain 1 to %d bytes", MaxFeedBytes)
	}
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = false
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid CSV: %w", err)
	}
	if len(records) < 2 || len(records)-1 > MaxFeedRows {
		return nil, fmt.Errorf("CSV must contain 1 to %d data rows", MaxFeedRows)
	}
	headers := make(map[string]int, len(records[0]))
	for index, header := range records[0] {
		header = strings.ToLower(strings.TrimSpace(header))
		if header == "" || headers[header] != 0 || (index != 0 && headers[header] == 0) {
			if _, exists := headers[header]; exists {
				return nil, fmt.Errorf("duplicate CSV header %q", header)
			}
		}
		headers[header] = index
	}
	required := []string{"model_id", "input_token_cost", "cached_input_token_cost", "output_token_cost", "request_fixed_cost", "currency", "unit", "effective_at"}
	for _, header := range required {
		if _, ok := headers[header]; !ok {
			return nil, fmt.Errorf("CSV header %q is required", header)
		}
	}
	changes := make([]domain.ProviderCostChangeRequest, 0, len(records)-1)
	for rowIndex, record := range records[1:] {
		value := func(name string) string {
			index, ok := headers[name]
			if !ok || index >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[index])
		}
		unit, parseErr := strconv.ParseInt(value("unit"), 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("CSV row %d: unit must be an integer", rowIndex+2)
		}
		row := feedRow{ModelID: value("model_id"), InputTokenCost: value("input_token_cost"), CachedInputTokenCost: value("cached_input_token_cost"),
			OutputTokenCost: value("output_token_cost"), RequestFixedCost: value("request_fixed_cost"), Currency: value("currency"), Unit: unit,
			EffectiveAt: value("effective_at")}
		if expires := value("expires_at"); expires != "" {
			row.ExpiresAt = &expires
		}
		key := value("idempotency_key")
		if key == "" {
			key = rowKey(batchKey, rowIndex)
		}
		change, rowErr := row.change(providerID, "CSV", key, sourceReference)
		if rowErr != nil {
			return nil, fmt.Errorf("CSV row %d: %w", rowIndex+2, rowErr)
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (row feedRow) change(providerID, sourceType, key, sourceReference string) (domain.ProviderCostChangeRequest, error) {
	for _, value := range []string{row.ModelID, row.InputTokenCost, row.CachedInputTokenCost, row.OutputTokenCost, row.RequestFixedCost, row.Currency, row.EffectiveAt} {
		if len(value) > 200 || strings.HasPrefix(value, "=") || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "@") {
			return domain.ProviderCostChangeRequest{}, errors.New("formula-like or oversized field is not allowed")
		}
	}
	for _, amount := range []string{row.InputTokenCost, row.CachedInputTokenCost, row.OutputTokenCost, row.RequestFixedCost} {
		if err := pricing.ValidateStoredDecimal(amount); err != nil {
			return domain.ProviderCostChangeRequest{}, errors.New("costs must be non-negative decimal strings with at most 12 fractional digits")
		}
	}
	effectiveAt, err := time.Parse(time.RFC3339, row.EffectiveAt)
	if err != nil {
		return domain.ProviderCostChangeRequest{}, errors.New("effective_at must be RFC3339")
	}
	var expiresAt *time.Time
	if row.ExpiresAt != nil && strings.TrimSpace(*row.ExpiresAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(*row.ExpiresAt))
		if parseErr != nil || !parsed.After(effectiveAt) {
			return domain.ProviderCostChangeRequest{}, errors.New("expires_at must be RFC3339 and after effective_at")
		}
		expiresAt = &parsed
	}
	if providerID == "" || row.ModelID == "" || row.Unit <= 0 || len(row.Currency) != 3 || key == "" || len(key) > 200 {
		return domain.ProviderCostChangeRequest{}, errors.New("provider, model, currency, positive unit, and idempotency key are required")
	}
	return domain.ProviderCostChangeRequest{ProviderID: providerID, ModelID: row.ModelID, SourceType: sourceType, SourceReference: sourceReference,
		InputTokenCost: row.InputTokenCost, CachedInputTokenCost: row.CachedInputTokenCost, OutputTokenCost: row.OutputTokenCost,
		RequestFixedCost: row.RequestFixedCost, Currency: strings.ToUpper(row.Currency), Unit: row.Unit, EffectiveAt: effectiveAt.UTC(),
		ExpiresAt: expiresAt, IdempotencyKey: key}, nil
}

func validateSource(ctx context.Context, rawURL string, allowedHosts []string) (*url.URL, string, []net.IP, error) {
	endpoint, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Hostname() == "" || endpoint.Fragment != "" {
		return nil, "", nil, ErrUnsafeSource
	}
	host := strings.ToLower(endpoint.Hostname())
	allowed := false
	for _, candidate := range allowedHosts {
		if strings.EqualFold(strings.TrimSpace(candidate), host) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, "", nil, ErrUnsafeSource
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(resolved) == 0 {
		return nil, "", nil, ErrUnsafeSource
	}
	addresses := make([]net.IP, 0, len(resolved))
	for _, item := range resolved {
		if !isPublicIP(item.IP) {
			return nil, "", nil, ErrUnsafeSource
		}
		addresses = append(addresses, item.IP)
	}
	return endpoint, host, addresses, nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	// Go's IsPrivate intentionally excludes several non-global ranges. Pricing
	// fetches must also reject carrier-grade NAT, benchmarking, documentation,
	// protocol-assignment and IPv6 documentation/ULA ranges.
	blocked := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"::/128", "::1/128", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
	}
	for _, raw := range blocked {
		_, network, _ := net.ParseCIDR(raw)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func readBounded(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, MaxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxFeedBytes {
		return nil, fmt.Errorf("Provider pricing response exceeds %d bytes", MaxFeedBytes)
	}
	return body, nil
}

func safeReference(endpoint *url.URL, body []byte) string {
	clean := *endpoint
	clean.RawQuery, clean.ForceQuery = "", false
	sum := sha256.Sum256(body)
	return clean.String() + "#sha256=" + hex.EncodeToString(sum[:])
}

func rowKey(batchKey string, index int) string {
	return fmt.Sprintf("%s:%03d", strings.TrimSpace(batchKey), index+1)
}
