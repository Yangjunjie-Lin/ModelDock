package version

import (
	"strings"
	"testing"
)

func TestMetadataPreservesRelayDockCompatibility(t *testing.T) {
	metadata := Metadata()
	if metadata.Product != "ModelDock" {
		t.Fatalf("product = %q, want ModelDock", metadata.Product)
	}
	if metadata.CompatibilityName != "RelayDock" {
		t.Fatalf("compatibility name = %q, want RelayDock", metadata.CompatibilityName)
	}
	if metadata.Version != Current || metadata.Commit != Commit || metadata.BuildTime != BuildTime {
		t.Fatalf("metadata does not expose the configured build values: %#v", metadata)
	}
}

func TestStringIncludesAuditableBuildIdentity(t *testing.T) {
	got := String()
	for _, expected := range []string{"ModelDock", Current, "RelayDock compatibility", Commit, BuildTime} {
		if !strings.Contains(got, expected) {
			t.Fatalf("version string %q does not contain %q", got, expected)
		}
	}
}
