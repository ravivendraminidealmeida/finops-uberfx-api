package postgres

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var files embed.FS

func newMigrate(databaseURL string) (*migrate.Migrate, func(), error) {
	src, err := iofs.New(files, "migrations")
	if err != nil {
		return nil, nil, fmt.Errorf("migration source: %w", err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, nil, err
	}
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx/v5", driver)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	cleanup := func() { _, _ = m.Close(); _ = db.Close() }
	return m, cleanup, nil
}

func RunMigrations(databaseURL string) error {
	m, cleanup, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

func RollbackLast(databaseURL string) error {
	m, cleanup, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	defer cleanup()
	return m.Steps(-1)
}
