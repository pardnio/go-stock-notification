package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/lib/pq"
)

type db struct {
	host     string
	port     string
	user     string
	password string
	dbName   string
	sslMode  string
}

func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM,
	)
	defer cancel()

	config := db{
		host:     "localhost",
		port:     "5432",
		user:     "postgres",
		password: "password",
		dbName:   "database",
		sslMode:  "disable",
	}

	db, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.host, config.port, config.user, config.password, config.dbName, config.sslMode,
	))
	if err != nil {
		slog.Error("open db failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		slog.Error("ping db failed", "error", err)
		os.Exit(1)
	}

	slog.Info("database connected",
		"host", config.host,
		"port", config.port,
		"database", config.dbName,
	)
}
