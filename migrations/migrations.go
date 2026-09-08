// Package migrations applies database schema migrations embedded from the
// migrations/ directory. Migrations run in lexicographic filename order and
// are protected by a PostgreSQL advisory lock so concurrent instances do not
// race.
package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed *.sql
var migrationsFS embed.FS

const advisoryLockKey = 0x5650535f4d4947 // "VPS_MIG"

// Runner applies SQL migrations.
type Runner struct {
	conn *pgx.Conn
	log  *slog.Logger
}

// New connects to the database and prepares the migration runner.
func New(ctx context.Context, dsn string, log *slog.Logger) (*Runner, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect for migrations: %w", err)
	}
	return &Runner{conn: conn, log: log}, nil
}

// Close releases the underlying connection.
func (r *Runner) Close() {
	_ = r.conn.Close(context.Background())
}

// Up applies all pending migrations.
func (r *Runner) Up(ctx context.Context) error {
	if err := r.ensureSchemaMigrations(ctx); err != nil {
		return err
	}

	locked, err := r.conn.Exec(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey)
	if err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if locked.RowsAffected() == 0 {
		return fmt.Errorf("another migration process is running")
	}
	defer func() {
		_, _ = r.conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	applied, err := r.appliedMigrations(ctx)
	if err != nil {
		return err
	}

	files, err := r.listMigrationFiles()
	if err != nil {
		return err
	}

	for _, name := range files {
		if applied[name] {
			continue
		}
		content, err := migrationsFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := r.applyOne(ctx, name, string(content)); err != nil {
			return err
		}
		r.log.Info("migration applied", "name", name)
	}
	return nil
}

func (r *Runner) ensureSchemaMigrations(ctx context.Context) error {
	_, err := r.conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

func (r *Runner) appliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := r.conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func (r *Runner) listMigrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") || strings.HasPrefix(name, ".") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	return files, nil
}

func (r *Runner) applyOne(ctx context.Context, name, content string) error {
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	if _, err := tx.Exec(ctx, content); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// WaitForDB blocks until the database is reachable or the context expires.
func WaitForDB(ctx context.Context, dsn string, log *slog.Logger, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			_ = conn.Close(ctx)
			return nil
		}
		log.Warn("database not ready", "error", err.Error())
		select {
		case <-ctx.Done():
			return fmt.Errorf("database never became ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// EnsureDatabase creates the target database if it does not exist, so tests
// and tooling can run against a fresh server without manual setup. It connects
// to the "postgres" maintenance database on the same server.
func EnsureDatabase(ctx context.Context, dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	dbName := cfg.Database
	if dbName == "" {
		return fmt.Errorf("dsn has no database name")
	}
	// Point the connection at the maintenance database.
	cfg.Database = "postgres"

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect to postgres maintenance db: %w", err)
	}
	defer conn.Close(ctx)

	// CREATE DATABASE cannot run inside a transaction block; pgx auto-commits
	// a standalone statement.
	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize())
	if err != nil {
		// 42P04 = duplicate_database; ignore.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
			return nil
		}
		return fmt.Errorf("create database %s: %w", dbName, err)
	}
	return nil
}
