package supplier

import (
	"context"
	"encoding/hex"
	"net"
	"testing"
)

type fixedResolver map[string][]net.IP

func (r fixedResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	out := make([]net.IPAddr, 0, len(r[host]))
	for _, ip := range r[host] {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

func TestValidateEndpointRejectsPrivateAndNonHTTPS(t *testing.T) {
	resolver := fixedResolver{
		"public.example":  {net.ParseIP("203.0.113.10")},
		"private.example": {net.ParseIP("10.0.0.4")},
	}
	if _, _, err := ValidateEndpointURL(context.Background(), "http://public.example", resolver); err == nil {
		t.Fatal("expected HTTP endpoint to be rejected")
	}
	if _, _, err := ValidateEndpointURL(context.Background(), "https://private.example", resolver); err == nil {
		t.Fatal("expected private DNS answer to be rejected")
	}
	if _, _, err := ValidateEndpointURL(context.Background(), "https://public.example:8443", resolver); err == nil {
		t.Fatal("expected non-443 endpoint to be rejected")
	}
}

func TestValidateEndpointAcceptsPublicHTTPSAndChallengeIsConstantTimeComparable(t *testing.T) {
	resolver := fixedResolver{"public.example": {net.ParseIP("203.0.113.10")}}
	u, ips, err := ValidateEndpointURL(context.Background(), "https://public.example/api", resolver)
	if err != nil {
		t.Fatalf("expected public endpoint: %v", err)
	}
	if u.Scheme != "https" || len(ips) != 1 || ips[0].String() != "203.0.113.10" {
		t.Fatalf("unexpected validated endpoint: %s %#v", u, ips)
	}
	hash := ChallengeHash("one-time-challenge")
	if !VerifyChallenge(hash, "one-time-challenge") || VerifyChallenge(hash, "wrong") {
		t.Fatal("challenge verification did not enforce exact token")
	}
	if !VerifyChallengeResponse(hash, ""+hex.EncodeToString(hash)) || VerifyChallengeResponse(hash, "one-time-challenge") {
		t.Fatal("challenge response must be the stored hash representation")
	}
}
