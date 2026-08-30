package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const oidcDiscoveryResponseLimit = 1 << 20

func validateOIDCIssuer(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("OIDC issuer must be a public HTTPS URL without credentials, query, or fragment")
	}
	if strings.EqualFold(parsed.Hostname(), "localhost") {
		return errors.New("OIDC issuer cannot use localhost")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !publicOIDCAddress(ip) {
		return errors.New("OIDC issuer cannot use a private or special-purpose address")
	}
	return nil
}

func publicOIDCAddress(ip net.IP) bool {
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func testOIDCDiscovery(ctx context.Context, issuer string) (map[string]any, error) {
	if err := validateOIDCIssuer(issuer); err != nil {
		return nil, err
	}
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	discoveryURL := issuer + "/.well-known/openid-configuration"
	dialer := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 15 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   4 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second,
		DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIP(dialCtx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, fmt.Errorf("resolve OIDC issuer: %w", err)
			}
			for _, addressIP := range addresses {
				if !publicOIDCAddress(addressIP) {
					return nil, errors.New("OIDC issuer resolved to a private or special-purpose address")
				}
			}
			return dialer.DialContext(dialCtx, network, net.JoinHostPort(addresses[0].String(), port))
		},
	}
	client := &http.Client{Timeout: 6 * time.Second, Transport: transport, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("OIDC discovery redirected too many times")
		}
		return validateOIDCIssuer(request.URL.Scheme + "://" + request.URL.Host)
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("OIDC discovery returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, oidcDiscoveryResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > oidcDiscoveryResponseLimit {
		return nil, errors.New("OIDC discovery document is too large")
	}
	var document struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		JWKSURI               string   `json:"jwks_uri"`
		ScopesSupported       []string `json:"scopes_supported"`
	}
	if json.Unmarshal(raw, &document) != nil || strings.TrimRight(document.Issuer, "/") != issuer ||
		document.AuthorizationEndpoint == "" || document.TokenEndpoint == "" || document.JWKSURI == "" {
		return nil, errors.New("OIDC discovery document is incomplete or its issuer does not match")
	}
	return map[string]any{"status": "ok", "issuer": document.Issuer, "authorization_endpoint": document.AuthorizationEndpoint,
		"token_endpoint": document.TokenEndpoint, "jwks_uri": document.JWKSURI, "scopes_supported": document.ScopesSupported,
		"tested_at": time.Now().UTC()}, nil
}
