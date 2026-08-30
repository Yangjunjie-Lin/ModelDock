package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/relayedock/relayedock/internal/domain"
)

var (
	ErrNoCredential     = errors.New("no eligible credential in pool")
	ErrStateUnavailable = errors.New("credential state service unavailable")
)

type CandidateSource interface {
	Candidates(context.Context, string) ([]domain.Credential, error)
}

type Counter interface {
	TryAcquire(context.Context, string, int) (bool, int64, error)
	Release(context.Context, string) error
	Active(context.Context, string) (int64, error)
}

type WeightedCandidate struct {
	ID     string
	Weight int
}

type WeightedChooser interface {
	ChooseWeighted(context.Context, string, []WeightedCandidate) (string, error)
}

type RedisCounter struct{ client *redis.Client }

func NewRedisCounter(client *redis.Client) *RedisCounter { return &RedisCounter{client: client} }

var acquireScript = redis.NewScript(`
local n=redis.call('INCR',KEYS[1])
if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[2]) end
if n>tonumber(ARGV[1]) then redis.call('DECR',KEYS[1]); return {-1,n-1} end
return {1,n}`)
var releaseScript = redis.NewScript(`
local n=tonumber(redis.call('GET',KEYS[1]) or '0')
if n<=1 then redis.call('DEL',KEYS[1]); return 0 end
return redis.call('DECR',KEYS[1])`)
var weightedRoundRobinScript = redis.NewScript(`
local total=0
local best=nil
local bestScore=nil
for i=1,#ARGV,2 do
  local id=ARGV[i]
  local weight=tonumber(ARGV[i+1])
  total=total+weight
  local score=tonumber(redis.call('HGET',KEYS[1],id) or '0')+weight
  redis.call('HSET',KEYS[1],id,score)
  if bestScore==nil or score>bestScore or (score==bestScore and id<best) then
    best=id
    bestScore=score
  end
end
if best==nil then return '' end
redis.call('HINCRBY',KEYS[1],best,-total)
redis.call('EXPIRE',KEYS[1],86400)
return best`)

func (r *RedisCounter) TryAcquire(ctx context.Context, id string, max int) (bool, int64, error) {
	v, err := acquireScript.Run(ctx, r.client, []string{"rdk:active:" + id}, max, 7200).Slice()
	if err != nil {
		return false, 0, err
	}
	ok, _ := v[0].(int64)
	active, _ := v[1].(int64)
	return ok == 1, active, nil
}
func (r *RedisCounter) Release(ctx context.Context, id string) error {
	return releaseScript.Run(ctx, r.client, []string{"rdk:active:" + id}).Err()
}
func (r *RedisCounter) Active(ctx context.Context, id string) (int64, error) {
	n, err := r.client.Get(ctx, "rdk:active:"+id).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}
func (r *RedisCounter) ChooseWeighted(ctx context.Context, groupID string, candidates []WeightedCandidate) (string, error) {
	args := make([]any, 0, len(candidates)*2)
	for _, candidate := range candidates {
		args = append(args, candidate.ID, strconv.Itoa(max(candidate.Weight, 1)))
	}
	return weightedRoundRobinScript.Run(ctx, r.client, []string{"rdk:wrr:" + groupID}, args...).Text()
}

type Scheduler struct {
	source   CandidateSource
	counters Counter
}

func New(source CandidateSource, counters Counter) *Scheduler {
	return &Scheduler{source: source, counters: counters}
}

type Selection struct {
	Credential domain.Credential `json:"credential"`
	Reason     map[string]any    `json:"reason"`
	release    func()
}

type CredentialConstraints struct {
	RequiredTags      []string `json:"required_credential_tags"`
	ExcludedTags      []string `json:"excluded_credential_tags"`
	Model             string   `json:"model,omitempty"`
	APIKeyID          string   `json:"api_key_id,omitempty"`
	MemberID          string   `json:"member_id,omitempty"`
	UseSharedCapacity *bool    `json:"use_shared_capacity,omitempty"`
}

func (s *Selection) Release() {
	if s.release != nil {
		s.release()
		s.release = nil
	}
}

func (s *Scheduler) Select(ctx context.Context, groupID string, requestedPolicy ...string) (*Selection, error) {
	policy := "priority_weighted"
	if len(requestedPolicy) > 0 {
		policy = requestedPolicy[0]
	}
	return s.SelectConstrained(ctx, groupID, policy, CredentialConstraints{})
}

func (s *Scheduler) SelectConstrained(ctx context.Context, groupID, requestedPolicy string, constraints CredentialConstraints) (*Selection, error) {
	return s.SelectConstrainedForOrganization(ctx, groupID, requestedPolicy, constraints, "")
}

func (s *Scheduler) SelectConstrainedForOrganization(ctx context.Context, groupID, requestedPolicy string, constraints CredentialConstraints, organizationID string) (*Selection, error) {
	policy := "priority_weighted"
	if requestedPolicy != "" {
		switch requestedPolicy {
		case "least_loaded":
			policy = "least_loaded"
		case "weighted_round_robin":
			policy = "weighted_round_robin"
		case "priority_weighted", "weighted_least_load", "weighted", "priority":
			policy = "priority_weighted"
		}
	}
	candidates, err := s.source.Candidates(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrNoCredential
	}
	type ranked struct {
		c      domain.Credential
		active int64
		score  float64
		tier   int
	}
	blockShared := constraints.UseSharedCapacity != nil && !*constraints.UseSharedCapacity
	for _, candidate := range candidates {
		if candidate.CredentialOwner != domain.CredentialOwnerCustomer || candidate.OwnerOrganizationID == nil ||
			*candidate.OwnerOrganizationID != organizationID || !credentialActorFiltersMatch(candidate, constraints) {
			continue
		}
		switch candidate.SharedCapacityFallback {
		case "NEVER":
			blockShared = true
		case "OUTSIDE_FILTERS":
			if stringFilterMatches(candidate.ModelFilters, constraints.Model) {
				blockShared = true
			}
		}
	}
	list := make([]ranked, 0, len(candidates))
	for _, c := range candidates {
		if c.OrganizationID != nil && *c.OrganizationID != organizationID {
			continue
		}
		if c.CredentialOwner == domain.CredentialOwnerCustomer && (c.OwnerOrganizationID == nil || *c.OwnerOrganizationID != organizationID) {
			continue
		}
		if c.CredentialOwner == domain.CredentialOwnerPlatform && blockShared {
			continue
		}
		if !credentialFiltersMatch(c, constraints) {
			continue
		}
		if !matchesCredentialTags(c.Tags, constraints) {
			continue
		}
		active, err := s.counters.Active(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStateUnavailable, err)
		}
		if active >= int64(c.MaxConcurrency) {
			continue
		}
		weight := c.EffectiveWeight
		if weight <= 0 {
			weight = c.Weight
		}
		priority := c.EffectivePriority
		if priority == 0 {
			priority = c.Priority
		}
		score := float64(weight) / float64(active+1)
		c.ActiveRequests = active
		c.EffectiveWeight = weight
		c.EffectivePriority = priority
		tier := 2
		if c.CredentialOwner == domain.CredentialOwnerCustomer {
			tier = 3
			if c.BYOKPrioritySection == "FALLBACK" {
				tier = 1
			}
		}
		list = append(list, ranked{c: c, active: active, score: score, tier: tier})
	}
	if policy == "least_loaded" {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].tier != list[j].tier {
				return list[i].tier > list[j].tier
			}
			iLoad := float64(list[i].active) / float64(max(list[i].c.MaxConcurrency, 1))
			jLoad := float64(list[j].active) / float64(max(list[j].c.MaxConcurrency, 1))
			if iLoad != jLoad {
				return iLoad < jLoad
			}
			if list[i].c.EffectivePriority != list[j].c.EffectivePriority {
				return list[i].c.EffectivePriority > list[j].c.EffectivePriority
			}
			return list[i].c.ID < list[j].c.ID
		})
	} else {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].tier != list[j].tier {
				return list[i].tier > list[j].tier
			}
			if list[i].c.EffectivePriority != list[j].c.EffectivePriority {
				return list[i].c.EffectivePriority > list[j].c.EffectivePriority
			}
			if list[i].score != list[j].score {
				return list[i].score > list[j].score
			}
			return list[i].c.ID < list[j].c.ID
		})
	}
	if policy == "weighted_round_robin" && len(list) > 1 {
		topPriority := list[0].c.EffectivePriority
		topTier := list[0].tier
		weighted := make([]WeightedCandidate, 0, len(list))
		for _, item := range list {
			if item.tier != topTier || item.c.EffectivePriority != topPriority {
				break
			}
			weighted = append(weighted, WeightedCandidate{ID: item.c.ID, Weight: item.c.EffectiveWeight})
		}
		if chooser, ok := s.counters.(WeightedChooser); ok {
			chosen, err := chooser.ChooseWeighted(ctx, groupID, weighted)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrStateUnavailable, err)
			}
			for index := range list {
				if list[index].c.ID == chosen {
					list[0], list[index] = list[index], list[0]
					break
				}
			}
		}
	}
	for _, r := range list {
		ok, active, err := s.counters.TryAcquire(ctx, r.c.ID, r.c.MaxConcurrency)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStateUnavailable, err)
		}
		if !ok {
			continue
		}
		selected := r.c
		selected.ActiveRequests = active
		selection := &Selection{Credential: selected, Reason: map[string]any{"healthy": selected.CurrentHealth != "UNHEALTHY", "cooldown": false, "active_requests": active, "max_concurrency": selected.MaxConcurrency, "weight": selected.EffectiveWeight, "priority": selected.EffectivePriority, "score": r.score, "policy": policy, "credential_tags": selected.Tags, "required_credential_tags": constraints.RequiredTags, "excluded_credential_tags": constraints.ExcludedTags, "credential_owner": selected.CredentialOwner, "byok_priority_section": selected.BYOKPrioritySection, "shared_capacity_fallback": selected.SharedCapacityFallback, "shared_capacity_blocked": blockShared}}
		selection.release = func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.counters.Release(releaseCtx, selected.ID)
		}
		return selection, nil
	}
	return nil, ErrNoCredential
}

func credentialActorFiltersMatch(credential domain.Credential, constraints CredentialConstraints) bool {
	return stringFilterMatches(credential.APIKeyFilters, constraints.APIKeyID) &&
		stringFilterMatches(credential.MemberFilters, constraints.MemberID)
}

func credentialFiltersMatch(credential domain.Credential, constraints CredentialConstraints) bool {
	return credentialActorFiltersMatch(credential, constraints) && stringFilterMatches(credential.ModelFilters, constraints.Model)
}

func stringFilterMatches(filters []string, value string) bool {
	if len(filters) == 0 {
		return true
	}
	value = strings.ToLower(strings.TrimSpace(value))
	for _, filter := range filters {
		if strings.ToLower(strings.TrimSpace(filter)) == value {
			return true
		}
	}
	return false
}

func matchesCredentialTags(tags []string, constraints CredentialConstraints) bool {
	available := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		available[tag] = struct{}{}
	}
	for _, required := range constraints.RequiredTags {
		if _, ok := available[required]; !ok {
			return false
		}
	}
	for _, excluded := range constraints.ExcludedTags {
		if _, ok := available[excluded]; ok {
			return false
		}
	}
	return true
}
