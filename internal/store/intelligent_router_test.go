package store

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
)

func TestQualityTrafficRampIsDeterministicAndBounded(t *testing.T) {
	providerID, routeID := "provider-a", "route-a"
	admitted := 0
	for index := 0; index < 10000; index++ {
		requestID := fmt.Sprintf("request-%d", index)
		first := qualityTrafficAdmitted(requestID, providerID, routeID, 500)
		second := qualityTrafficAdmitted(requestID, providerID, routeID, 500)
		if first != second {
			t.Fatal("ramp admission was not deterministic")
		}
		if first {
			admitted++
		}
	}
	if admitted < 350 || admitted > 650 {
		t.Fatalf("500 bps ramp admitted %d/10000 requests", admitted)
	}
	if qualityTrafficAdmitted("request", providerID, routeID, 0) || !qualityTrafficAdmitted("request", providerID, routeID, 10000) {
		t.Fatal("ramp boundary behavior is incorrect")
	}
}

func TestChooseCandidateStrategies(t *testing.T) {
	candidates := []routeCandidate{
		{route: domain.ProjectModelRoute{ID: "cheap", Alias: "cheap"}, inputPrice: "1", outputPrice: "2", hasPrice: true, quality: 70, latency: 30},
		{route: domain.ProjectModelRoute{ID: "best", Alias: "best"}, inputPrice: "8", outputPrice: "12", hasPrice: true, quality: 98, latency: 20},
		{route: domain.ProjectModelRoute{ID: "fast", Alias: "fast"}, inputPrice: "4", outputPrice: "6", hasPrice: true, quality: 80, latency: 5},
	}

	tests := []struct {
		name     string
		rule     domain.RoutingRule
		expected string
	}{
		{name: "cost", rule: domain.RoutingRule{Strategy: "cost_optimized"}, expected: "cheap"},
		{name: "quality", rule: domain.RoutingRule{Strategy: "quality_optimized"}, expected: "best"},
		{name: "balanced", rule: domain.RoutingRule{Strategy: "balanced", QualityWeight: .5, PriceWeight: .3, LatencyWeight: .2}, expected: "cheap"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected, err := chooseCandidate(candidates, test.rule)
			if err != nil {
				t.Fatal(err)
			}
			if selected.route.ID != test.expected {
				t.Fatalf("expected %s, got %s (score %f)", test.expected, selected.route.ID, selected.score)
			}
		})
	}
}

func TestCandidateMatchesConstraints(t *testing.T) {
	candidate := routeCandidate{route: domain.ProjectModelRoute{ProviderID: "provider-id", ProviderType: "gemini"},
		modelType: "text", capabilities: []string{"chat", "vision"}, inputPrice: "2", outputPrice: "5", hasPrice: true}
	if !candidateMatches(candidate, map[string]any{"providers": []any{"gemini"}, "required_capabilities": []any{"vision"}, "max_output_price": json.Number("5")}) {
		t.Fatal("expected candidate to match")
	}
	if candidateMatches(candidate, map[string]any{"required_capabilities": []any{"audio"}}) {
		t.Fatal("candidate with missing capability must not match")
	}
}

func TestCostOptimizedRequiresConfiguredPrice(t *testing.T) {
	_, err := chooseCandidate([]routeCandidate{{route: domain.ProjectModelRoute{ID: "unpriced"}, quality: 90}},
		domain.RoutingRule{Strategy: "cost_optimized"})
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for an unpriced cost route, got %v", err)
	}
}
