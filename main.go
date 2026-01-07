package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
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

type Notification struct {
	Price     float64 `json:"price"`
	Ticker    string  `json:"ticker"`
	Content   string  `json:"content"`
	Direction string  `json:"direction"`
}

func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM,
	)
	defer cancel()

	config := db{
		host:     "postgres",
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

	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/set/:ticker/notify/:price", setNotify)
	r.GET("/set/:ticker/price/:price", setPrice)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	go func() {
		slog.Info("server started",
			"address", ":8080",
			"path", map[string]string{
				"setPrice":  "/set/:ticker/notify/:price",
				"setNotify": "/set/:ticker/price/:price",
			},
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			cancel()
		}
	}()

	slog.Info("listening", "channel", notifyKey)

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := srv.Shutdown(shutdownCtx); err != nil {
				slog.Error("server shutdown failed", "error", err)
			}
			shutdownCancel()
			slog.Info("server stopped")
			os.Exit(0)
		case notification := <-listener.Notify:
			if notification == nil {
				os.Exit(1)
			}

			var n Notification
			if err := json.Unmarshal([]byte(notification.Extra), &n); err != nil {
				slog.Error("unmarshal notification failed", "error", err, "payload", notification.Extra)
				continue
			}
			slog.Info("received notification",
				"ticker", n.Ticker,
				"content", n.Content,
				"direction", n.Direction,
				"price", n.Price,
			)

			sendToDiscord(
				fmt.Sprintf("%s is %s", n.Ticker, n.Content),
				"",
			)
		case <-time.After(30 * time.Second):
			if err := listener.Ping(); err != nil {
				slog.Error("ping failed", "error", err)
			}
		}
	}
}

func setPrice(c *gin.Context) {
	ticker := c.Param("ticker")
	price := c.Param("price")
	if ticker == "" || price == "" {
		slog.Error("ticker / price is required")
		c.String(http.StatusBadRequest, "ticker / price is required")
		return
	}

	priceFloat, err := strconv.ParseFloat(price, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid price")
		return
	}

	_, err = DB.ExecContext(c.Request.Context(), `
	INSERT INTO ticker_price (ticker, new_price)
	VALUES ($1, $2)
	ON CONFLICT (ticker) DO UPDATE
	SET new_price = EXCLUDED.new_price
	`, ticker, priceFloat)
	if err != nil {
		slog.Error("set price failed", "ticker", ticker, "error", err)
		c.String(http.StatusInternalServerError, "database error")
		return
	}

	sendToDiscord(
		"",
		fmt.Sprintf("Set %s new price to %f", ticker, priceFloat),
	)

	c.String(http.StatusOK, "ok")
}

func setNotify(c *gin.Context) {
	ticker := c.Param("ticker")
	price := c.Param("price")
	if ticker == "" || price == "" {
		slog.Error("ticker / price is required")
		c.String(http.StatusBadRequest, "ticker / price is required")
		return
	}

	priceFloat, err := strconv.ParseFloat(price, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid price")
		return
	}

	_, err = DB.ExecContext(c.Request.Context(), `
	INSERT INTO ticker_compare (ticker, price)
	VALUES ($1, $2)
	ON CONFLICT (ticker) DO UPDATE
	SET price = EXCLUDED.price
	`, ticker, priceFloat)
	if err != nil {
		slog.Error("set compare failed", "ticker", ticker, "error", err)
		c.String(http.StatusInternalServerError, "database error")
		return
	}

	sendToDiscord(
		"",
		fmt.Sprintf("Set %s notify price to %f", ticker, priceFloat),
	)

	c.String(http.StatusOK, "ok")
}

type DiscordWebhook struct {
	Embeds []Embed `json:"embeds"`
}

type Embed struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	Color       int       `json:"color"`
	Fields      []Field   `json:"fields"`
	Thumbnail   Thumbnail `json:"thumbnail"`
	Footer      Footer    `json:"footer"`
	Timestamp   string    `json:"timestamp"`
}

type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type Thumbnail struct {
	URL string `json:"url"`
}

type Footer struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url"`
}

func sendToDiscord(title, description string) {

	webhook := DiscordWebhook{
		Embeds: []Embed{
			{
				Title: title,
				// URL:         "",
				Description: description,
				// Color:       3447003,
				// Fields: []Field{
				// 	{
				// 		Name:   "欄位標題",
				// 		Value:  "欄位內容 (支持基本的 md)",
				// 		Inline: true,
				// 	},
				// },
				// Thumbnail: Thumbnail{
				// 	URL: "",
				// },
				// Footer: Footer{
				// 	Text:    "",
				// 	IconURL: "",
				// },
				Timestamp: time.Now().Format(time.RFC3339),
			},
		},
	}

	jsonData, err := json.Marshal(webhook)
	if err != nil {
		panic(err)
	}

	resp, err := http.Post(
		"https://discord.com/api/webhooks/1458356278182281241/17_gcjs72KoXBMpJCF5_wjTA8abqJ9SRtPkww5fvWjeLoMWb4bpSIzSl1U2dnVvUBunM",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}
