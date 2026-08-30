package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/relayedock/relayedock/internal/domain"
)

func requestProviderPolicy(payload map[string]any) (domain.RequestProviderPolicy, error) {
	policy := domain.RequestProviderPolicy{}
	raw, exists := payload["provider"]
	if !exists || raw == nil {
		return policy, nil
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return policy, errors.New("provider must be an object")
	}
	policy.Order = stringList(value["order"])
	policy.Only = stringList(value["only"])
	policy.Ignore = stringList(value["ignore"])
	policy.Quantizations = stringList(value["quantizations"])
	policy.RequiredCapabilities = stringList(value["required_capabilities"])
	policy.ProcessingRegions = stringList(firstAny(value["processing_regions"], value["regions"]))
	if boolean, present := optionalBool(value["allow_fallbacks"]); present {
		policy.AllowFallbacks = &boolean
	}
	if boolean, present := optionalBool(value["use_shared_capacity"]); present {
		policy.UseSharedCapacity = &boolean
	}
	policy.RequireParameters, _ = value["require_parameters"].(bool)
	policy.ZDR, _ = value["zdr"].(bool)
	policy.DataCollection, _ = value["data_collection"].(string)
	policy.DataCollection = strings.ToLower(strings.TrimSpace(policy.DataCollection))
	if policy.DataCollection != "" && policy.DataCollection != "allow" && policy.DataCollection != "deny" {
		return policy, errors.New("provider.data_collection must be allow or deny")
	}
	switch sortValue := value["sort"].(type) {
	case string:
		policy.Sort = strings.ToLower(strings.TrimSpace(sortValue))
	case map[string]any:
		policy.Sort, _ = sortValue["by"].(string)
		policy.Sort = strings.ToLower(strings.TrimSpace(policy.Sort))
	}
	if policy.Sort != "" && policy.Sort != "price" && policy.Sort != "latency" && policy.Sort != "throughput" {
		return policy, errors.New("provider.sort must be price, latency, or throughput")
	}
	if maxPrice, ok := value["max_price"].(map[string]any); ok {
		if decimal, present, err := optionalDecimal(firstAny(maxPrice["prompt"], maxPrice["input"])); err != nil {
			return policy, fmt.Errorf("provider.max_price.prompt: %w", err)
		} else if present {
			policy.MaxInputPrice = &decimal
		}
		if decimal, present, err := optionalDecimal(firstAny(maxPrice["completion"], maxPrice["output"])); err != nil {
			return policy, fmt.Errorf("provider.max_price.completion: %w", err)
		} else if present {
			policy.MaxOutputPrice = &decimal
		}
	}
	if decimal, present, err := optionalDecimal(value["preferred_min_throughput"]); err != nil {
		return policy, fmt.Errorf("provider.preferred_min_throughput: %w", err)
	} else if present {
		policy.PreferredMinThroughput = &decimal
	}
	if milliseconds, present, err := optionalMilliseconds(value["preferred_max_latency"]); err != nil {
		return policy, fmt.Errorf("provider.preferred_max_latency: %w", err)
	} else if present {
		policy.PreferredMaxLatencyMS = &milliseconds
	}
	return policy, nil
}

func providerPolicyWithRequestCapabilities(policy domain.RequestProviderPolicy, payload map[string]any) domain.RequestProviderPolicy {
	required := append([]string(nil), policy.RequiredCapabilities...)
	if _, ok := payload["tools"]; ok {
		required = append(required, "tool_calling")
	}
	if _, ok := payload["response_format"]; ok {
		required = append(required, "structured_outputs")
	}
	if modalities := stringList(payload["modalities"]); len(modalities) > 0 {
		required = append(required, modalities...)
	}
	policy.RequiredCapabilities = uniqueLower(required)
	return policy
}

func mergeProviderPolicies(workspace, request domain.RequestProviderPolicy, allowedRegions []string, privacy map[string]any) domain.RequestProviderPolicy {
	out := workspace
	out.Ignore = uniqueLower(append(out.Ignore, request.Ignore...))
	out.RequiredCapabilities = uniqueLower(append(out.RequiredCapabilities, request.RequiredCapabilities...))
	out.Only = restrictiveStrings(out.Only, request.Only)
	out.ProcessingRegions = restrictiveStrings(restrictiveStrings(out.ProcessingRegions, allowedRegions), request.ProcessingRegions)
	if len(request.Order) > 0 {
		out.Order = request.Order
	}
	if len(request.Quantizations) > 0 {
		out.Quantizations = restrictiveStrings(out.Quantizations, request.Quantizations)
	}
	if request.Sort != "" {
		out.Sort = request.Sort
	}
	out.RequireParameters = out.RequireParameters || request.RequireParameters
	out.ZDR = out.ZDR || request.ZDR || mapBool(privacy, "zdr")
	if strings.EqualFold(out.DataCollection, "deny") || strings.EqualFold(request.DataCollection, "deny") || strings.EqualFold(mapString(privacy, "data_collection"), "deny") {
		out.DataCollection = "deny"
	} else if request.DataCollection != "" {
		out.DataCollection = request.DataCollection
	}
	out.AllowFallbacks = restrictiveBool(out.AllowFallbacks, request.AllowFallbacks, true)
	out.UseSharedCapacity = restrictiveBool(out.UseSharedCapacity, request.UseSharedCapacity, true)
	out.MaxInputPrice = lowerDecimal(out.MaxInputPrice, request.MaxInputPrice)
	out.MaxOutputPrice = lowerDecimal(out.MaxOutputPrice, request.MaxOutputPrice)
	if request.PreferredMinThroughput != nil {
		out.PreferredMinThroughput = request.PreferredMinThroughput
	}
	if request.PreferredMaxLatencyMS != nil {
		out.PreferredMaxLatencyMS = request.PreferredMaxLatencyMS
	}
	return out
}

func requestedModelFallbacks(payload map[string]any, primary string, allowFallbacks bool) []string {
	models := []string{strings.TrimSpace(primary)}
	if allowFallbacks {
		models = append(models, stringList(payload["models"])...)
	}
	return uniqueLowerPreserve(models)
}

func providerPolicySnapshot(policy domain.RequestProviderPolicy, requestedModels []string) map[string]any {
	raw, _ := json.Marshal(policy)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["requested_models"] = requestedModels
	return out
}

func optionalBool(value any) (bool, bool) {
	parsed, ok := value.(bool)
	return parsed, ok
}

func optionalDecimal(value any) (domain.Decimal, bool, error) {
	if value == nil {
		return "", false, nil
	}
	text := ""
	switch v := value.(type) {
	case string:
		text = v
	case json.Number:
		text = v.String()
	default:
		return "", false, errors.New("must be a decimal number")
	}
	parsed, err := domain.ParseDecimal(text)
	if err != nil {
		return "", false, err
	}
	negative, err := parsed.IsNegative()
	if err != nil || negative {
		return "", false, errors.New("must be non-negative")
	}
	return parsed, true, nil
}

func optionalMilliseconds(value any) (int64, bool, error) {
	decimal, present, err := optionalDecimal(value)
	if err != nil || !present {
		return 0, present, err
	}
	seconds, ok := new(big.Rat).SetString(decimal.String())
	if !ok {
		return 0, false, errors.New("must be a decimal number")
	}
	seconds.Mul(seconds, big.NewRat(1000, 1))
	milliseconds := new(big.Int).Quo(seconds.Num(), seconds.Denom())
	if !milliseconds.IsInt64() {
		return 0, false, errors.New("is out of range")
	}
	return milliseconds.Int64(), true, nil
}

func firstAny(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func restrictiveBool(left, right *bool, defaultValue bool) *bool {
	value := defaultValue
	if left != nil {
		value = *left
	}
	if right != nil && !*right {
		value = false
	} else if left == nil && right != nil {
		value = *right
	}
	return &value
}

func restrictiveStrings(left, right []string) []string {
	left = uniqueLower(left)
	right = uniqueLower(right)
	if len(left) == 0 {
		return right
	}
	if len(right) == 0 {
		return left
	}
	out := make([]string, 0)
	for _, value := range left {
		for _, candidate := range right {
			if value == candidate {
				out = append(out, value)
			}
		}
	}
	return out
}

func uniqueLower(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueLowerPreserve(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func lowerDecimal(left, right *domain.Decimal) *domain.Decimal {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	comparison, err := left.Compare(*right)
	if err == nil && comparison <= 0 {
		return left
	}
	return right
}

func mapBool(value map[string]any, key string) bool {
	parsed, _ := value[key].(bool)
	return parsed
}

func mapString(value map[string]any, key string) string {
	parsed, _ := value[key].(string)
	return parsed
}
