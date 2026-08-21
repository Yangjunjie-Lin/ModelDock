package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/relayedock/relayedock/migrations"
)

// TestObservabilityMigrationUpgradeIntegration constructs a database at the
// released V16 ledger, then proves the current migrator applies only V17.
func TestObservabilityMigrationUpgradeIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_MIGRATION_UPGRADE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_MIGRATION_UPGRADE_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version bigint PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations.All[:16] {
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
			t.Fatalf("seed V16 migration %d: %v", migration.Version, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatalf("commit V16 migration %d: %v", migration.Version, err)
		}
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var versionCount, supportTables, requiredColumns int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version=17`).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('status_events','observability_slos','support_tickets','support_ticket_messages')`).Scan(&supportTables); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND (table_name,column_name) IN (('request_logs','trace_id'),('alerts','dedupe_key'),('alerts','details'),('alerts','resolved_at'),('alerts','last_seen_at'))`).Scan(&requiredColumns); err != nil {
		t.Fatal(err)
	}
	if versionCount != 1 || supportTables != 4 || requiredColumns != 5 {
		t.Fatalf("version17=%d tables=%d columns=%d", versionCount, supportTables, requiredColumns)
	}
}
