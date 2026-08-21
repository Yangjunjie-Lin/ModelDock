package store

import (
	"context"
	"os"
	"testing"

	"github.com/relayedock/relayedock/internal/id"
)

func TestAuditHashChainAndImmutabilityIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	firstID, secondID := id.UUID(), id.UUID()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,action,resource_type,resource_id,after_state)
		VALUES($1,'security.chain.first','integration','first','{}'::jsonb)`, firstID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,action,resource_type,resource_id,after_state)
		VALUES($1,'security.chain.second','integration','second','{}'::jsonb)`, secondID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var firstHash, secondPrevious, secondHash []byte
	if err = s.pool.QueryRow(ctx, `SELECT entry_hash FROM audit_logs WHERE id=$1`, firstID).Scan(&firstHash); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT previous_hash,entry_hash FROM audit_logs WHERE id=$1`, secondID).Scan(&secondPrevious, &secondHash); err != nil {
		t.Fatal(err)
	}
	if len(firstHash) != 32 || len(secondHash) != 32 || string(secondPrevious) != string(firstHash) {
		t.Fatal("consecutive audit rows are not linked by SHA-256 hashes")
	}
	if _, err = s.pool.Exec(ctx, `UPDATE audit_logs SET action='tampered' WHERE id=$1`, firstID); err == nil {
		t.Fatal("audit mutation was not rejected")
	}
	if _, err = s.pool.Exec(ctx, `DELETE FROM audit_logs WHERE id=$1`, secondID); err == nil {
		t.Fatal("audit deletion was not rejected")
	}
}
