package main

import (
	"fmt"
	"os"

	"junglegaming.io/finops-api/internal/adapter/postgres"
	"junglegaming.io/finops-api/internal/platform/config"
)

func main() {
	dsn, err := config.DatabaseURL()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "up":
		err = postgres.RunMigrations(dsn)
	case "down":
		err = postgres.RollbackLast(dsn)
	default:
		fmt.Fprintln(os.Stderr, "usage: migrate [up|down]")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
