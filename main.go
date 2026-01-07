package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lib/pq"
)

const (
	notifyKey = "ticker_notify"
)

var (
	DB *sql.DB
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
	link := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.host, config.port, config.user, config.password, config.dbName, config.sslMode,
	)
	db, err := sql.Open("postgres", link)
	if err != nil {
		slog.Error("open db failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		slog.Error("ping db failed", "error", err)
		os.Exit(1)
	}

	DB = db

	slog.Info("database connected",
		"host", config.host,
		"port", config.port,
		"database", config.dbName,
	)

	listener := pq.NewListener(
		link,
		10*time.Second,
		time.Minute,
		func(ev pq.ListenerEventType, err error) {
			switch ev {
			case pq.ListenerEventConnected:
				slog.Info("listener connected")
			case pq.ListenerEventDisconnected:
				slog.Warn("listener disconnected", "error", err)
			case pq.ListenerEventReconnected:
				slog.Info("listener reconnected")
			case pq.ListenerEventConnectionAttemptFailed:
				slog.Error("connection attempt failed", "error", err)
			}
		})
	defer listener.Close()

	if err := listener.Listen(notifyKey); err != nil {
		slog.Error("listen failed", "error", err)
		os.Exit(1)
	}

	slog.Info("listening", "channel", notifyKey)

	for {
		select {
		case <-ctx.Done():
			os.Exit(1)
		case notification := <-listener.Notify:
			if notification == nil {
				os.Exit(1)
			}
		case <-time.After(30 * time.Second):
			if err := listener.Ping(); err != nil {
				slog.Error("ping failed", "error", err)
			}
		}
	}
}
