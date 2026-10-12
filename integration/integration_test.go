//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/example/meterflow/internal/platform"
)

func TestMigrateAndSeedAreRepeatable(t *testing.T) {
	ctx := context.Background()
	db, err := platform.OpenDB(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = platform.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = platform.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = platform.Seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = platform.Seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM plans`).Scan(&n); err != nil || n < 2 {
		t.Fatalf("plans=%d err=%v", n, err)
	}
}
