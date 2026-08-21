package supplier

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrUnsafeEndpoint = errors.New("supplier endpoint failed SSRF or network isolation checks")

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}
type defaultResolver struct{}

func (defaultResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func isPrivateOrSpecial(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// ValidateEndpointURL checks the URL and every current DNS answer. The same
// check is repeated immediately before dialing to reduce DNS rebinding risk.
func ValidateEndpointURL(ctx context.Context, raw string, resolver Resolver) (*url.URL, []net.IP, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return nil, nil, ErrUnsafeEndpoint
	}
	if u.Port() != "" && u.Port() != "443" {
		return nil, nil, ErrUnsafeEndpoint
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || strings.Contains(host, "[") {
		return nil, nil, ErrUnsafeEndpoint
	}
	if resolver == nil {
		resolver = defaultResolver{}
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, nil, fmt.Errorf("%w: DNS resolution failed", ErrUnsafeEndpoint)
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ip := address.IP
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		if isPrivateOrSpecial(ip) {
			return nil, nil, ErrUnsafeEndpoint
		}
		ips = append(ips, append(net.IP(nil), ip...))
	}
	u.Host = host
	if u.Path == "" {
		u.Path = "/"
	}
	return u, ips, nil
}

func ChallengeHash(token string) []byte { sum := sha256.Sum256([]byte(token)); return sum[:] }
func VerifyChallenge(hash []byte, token string) bool {
	actual := ChallengeHash(token)
	return len(hash) == len(actual) && subtle.ConstantTimeCompare(hash, actual) == 1
}

func VerifyChallengeResponse(hash []byte, response string) bool {
	expected := hex.EncodeToString(hash)
	actual := strings.TrimSpace(response)
	return len(expected) == len(actual) && subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

// VerifyEndpoint performs an ownership challenge over an isolated HTTPS
// client. Redirects are disabled and response bodies are bounded/discarded.
func VerifyEndpoint(ctx context.Context, rawURL, challenge string) (string, error) {
	u, ips, err := ValidateEndpointURL(ctx, rawURL, nil)
	if err != nil {
		return "", err
	}
	challengeURL := *u
	challengeURL.Path = strings.TrimRight(challengeURL.Path, "/") + "/.well-known/modeldock-endpoint-verification"
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	client.Transport = &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		DialContext: func(dialCtx context.Context, network, _ string) (net.Conn, error) {
			resolved, currentIPs, resolveErr := ValidateEndpointURL(dialCtx, rawURL, nil)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if len(resolved.Hostname()) == 0 || len(currentIPs) == 0 {
				return nil, ErrUnsafeEndpoint
			}
			for _, ip := range currentIPs {
				if isPrivateOrSpecial(ip) {
					return nil, ErrUnsafeEndpoint
				}
			}
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(dialCtx, network, net.JoinHostPort(currentIPs[0].String(), "443"))
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, challengeURL.String(), nil)
	if err != nil {
		return "", ErrUnsafeEndpoint
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("X-ModelDock-Challenge", challenge)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("endpoint verification failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK || !VerifyChallengeResponse(ChallengeHash(challenge), string(body)) {
		return "", errors.New("endpoint ownership challenge did not match")
	}
	return ips[0].String(), nil
}
