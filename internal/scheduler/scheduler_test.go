package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
)

type staticSource struct{ values []domain.Credential }

func (s staticSource) Candidates(context.Context, string) ([]domain.Credential, error) {
	return s.values, nil
}

type memoryCounter struct{ active map[string]int64 }

func (c *memoryCounter) TryAcquire(_ context.Context, credentialID string, maximum int) (bool, int64, error) {
	if c.active == nil {
		c.active = map[string]int64{}
	}
	if c.active[credentialID] >= int64(maximum) {
		return false, c.active[credentialID], nil
	}
	c.active[credentialID]++
	return true, c.active[credentialID], nil
}
func (c *memoryCounter) Release(_ context.Context, credentialID string) error {
	if c.active[credentialID] > 0 {
		c.active[credentialID]--
	}
	return nil
}
func (c *memoryCounter) Active(_ context.Context, credentialID string) (int64, error) {
	return c.active[credentialID], nil
}

func TestBYOKCapacitySectionsAndSharedFallback(t *testing.T) {
	organizationID := "organization-a"
	platform := domain.Credential{ID: "platform", CredentialOwner: domain.CredentialOwnerPlatform, Status: "ACTIVE",
		CurrentHealth: "HEALTHY", MaxConcurrency: 5, Weight: 100}
	byok := domain.Credential{ID: "byok", CredentialOwner: domain.CredentialOwnerCustomer, OwnerOrganizationID: &organizationID,
		Status: "ACTIVE", CurrentHealth: "HEALTHY", MaxConcurrency: 5, Weight: 100, BYOKPrioritySection: "PRIORITIZED",
		SharedCapacityFallback: "ALWAYS"}

	selectID := func(value domain.Credential) string {
		scheduler := New(staticSource{values: []domain.Credential{platform, value}}, &memoryCounter{active: map[string]int64{}})
		selection, err := scheduler.SelectConstrainedForOrganization(context.Background(), "group", "priority_weighted",
			CredentialConstraints{Model: "model-a"}, organizationID)
		if err != nil {
			t.Fatal(err)
		}
		defer selection.Release()
		return selection.Credential.ID
	}
	if selected := selectID(byok); selected != "byok" {
		t.Fatalf("prioritized BYOK did not precede shared capacity: %s", selected)
	}
	byok.BYOKPrioritySection = "FALLBACK"
	if selected := selectID(byok); selected != "platform" {
		t.Fatalf("fallback BYOK did not follow shared capacity: %s", selected)
	}
	byok.SharedCapacityFallback = "NEVER"
	if selected := selectID(byok); selected != "byok" {
		t.Fatalf("NEVER shared-capacity policy did not force matching BYOK: %s", selected)
	}
}

func TestBYOKFiltersAndRequestSharedCapacityBlock(t *testing.T) {
	organizationID := "organization-a"
	platform := domain.Credential{ID: "platform", CredentialOwner: domain.CredentialOwnerPlatform, Status: "ACTIVE",
		CurrentHealth: "HEALTHY", MaxConcurrency: 5, Weight: 100}
	byok := domain.Credential{ID: "byok", CredentialOwner: domain.CredentialOwnerCustomer, OwnerOrganizationID: &organizationID,
		Status: "ACTIVE", CurrentHealth: "HEALTHY", MaxConcurrency: 5, Weight: 100, BYOKPrioritySection: "PRIORITIZED",
		SharedCapacityFallback: "ALWAYS", ModelFilters: []string{"model-a"}, APIKeyFilters: []string{"key-a"},
		MemberFilters: []string{"member-a"}}
	shared := false
	scheduler := New(staticSource{values: []domain.Credential{platform, byok}}, &memoryCounter{active: map[string]int64{}})
	selection, err := scheduler.SelectConstrainedForOrganization(context.Background(), "group", "priority_weighted",
		CredentialConstraints{Model: "model-a", APIKeyID: "key-a", MemberID: "member-a", UseSharedCapacity: &shared}, organizationID)
	if err != nil || selection.Credential.ID != "byok" {
		t.Fatalf("matching filtered BYOK was not selected: selection=%+v err=%v", selection, err)
	}
	selection.Release()
	_, err = scheduler.SelectConstrainedForOrganization(context.Background(), "group", "priority_weighted",
		CredentialConstraints{Model: "model-b", APIKeyID: "key-a", MemberID: "member-a", UseSharedCapacity: &shared}, organizationID)
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("shared capacity or mismatched BYOK escaped request block: %v", err)
	}
}

func TestOrganizationScopedPlatformCredentialCannotCrossTenantOrMember(t *testing.T) {
	organizationID := "organization-a"
	credential := domain.Credential{ID: "managed", OrganizationID: &organizationID,
		CredentialOwner: domain.CredentialOwnerPlatform, Status: "ACTIVE", CurrentHealth: "HEALTHY",
		MaxConcurrency: 5, Weight: 100, MemberFilters: []string{"member-a"}}
	value := New(staticSource{values: []domain.Credential{credential}}, &memoryCounter{active: map[string]int64{}})
	if _, err := value.SelectConstrainedForOrganization(context.Background(), "group", "priority_weighted",
		CredentialConstraints{MemberID: "member-a"}, "organization-b"); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("cross-tenant scoped credential err=%v", err)
	}
	if _, err := value.SelectConstrainedForOrganization(context.Background(), "group", "priority_weighted",
		CredentialConstraints{MemberID: "member-b"}, organizationID); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("cross-member scoped credential err=%v", err)
	}
	selection, err := value.SelectConstrainedForOrganization(context.Background(), "group", "priority_weighted",
		CredentialConstraints{MemberID: "member-a"}, organizationID)
	if err != nil || selection.Credential.ID != credential.ID {
		t.Fatalf("owner selection=%+v err=%v", selection, err)
	}
}
