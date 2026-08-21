package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/relayedock/relayedock/migrations"
)

func TestProviderQualityMigrationUpgradeIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_PROVIDER_QUALITY_UPGRADE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_PROVIDER_QUALITY_UPGRADE_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version bigint PRIMARY KEY,name text NOT NULL,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations.All[:20] {
		tx, beginErr := s.pool.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, err = tx.Exec(ctx, migration.SQL); err == nil {
			sum := sha256.Sum256([]byte(migration.SQL))
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)`, migration.Version, migration.Name, hex.EncodeToString(sum[:]))
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("seed V20 migration %d: %v", migration.Version, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var versionCount, qualityTables, neutralStates int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version=21 AND name='provider_quality'`).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN
		('provider_quality_policies','provider_quality_states','supplier_provider_links','provider_quality_probe_schedules',
		'provider_quality_observations','provider_price_verifications','provider_quality_rollups','provider_sla_events')`).Scan(&qualityTables); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM providers p JOIN provider_quality_policies q ON q.provider_id=p.id
		JOIN provider_quality_states s ON s.provider_id=p.id WHERE NOT q.enabled AND s.grade='UNKNOWN'
		AND s.routing_multiplier=1 AND s.traffic_cap_bps=10000 AND s.circuit_state='CLOSED'`).Scan(&neutralStates); err != nil {
		t.Fatal(err)
	}
	var providerCount int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM providers`).Scan(&providerCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 1 || qualityTables != 8 || neutralStates != providerCount {
		t.Fatalf("version=%d tables=%d neutral=%d providers=%d", versionCount, qualityTables, neutralStates, providerCount)
	}
}
