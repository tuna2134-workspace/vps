// Package integration contains tests that run against a real PostgreSQL
// database. They use the TEST_DATABASE_URL environment variable (defaults to
// localhost) and require the migrations to be applied.
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tuna2134/vps/internal/controlplane/database"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
	"github.com/tuna2134/vps/migrations"
)

const defaultDSN = "postgres://postgres:postgres@localhost:5432/vps_test?sslmode=disable"

var (
	testPool *pgxpool.Pool
	repos    *repositories.Repositories
)

// TestMain connects to the test database and applies migrations once.
func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = defaultDSN
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := migrations.WaitForDB(ctx, dsn, testLogger(), time.Second); err != nil {
		panic(err)
	}
	runner, err := migrations.New(ctx, dsn, testLogger())
	if err != nil {
		panic(err)
	}
	if err := runner.Up(ctx); err != nil {
		runner.Close()
		panic(err)
	}
	runner.Close()

	pool, err := database.NewPool(ctx, dsn)
	if err != nil {
		panic(err)
	}
	testPool = pool
	repos = repositories.New(pool)

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// newContext returns a context with a per-test timeout.
func newContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}
