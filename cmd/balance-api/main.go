package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gchernikov/wallet_demo/internal/model"
	mysqlrepo "github.com/gchernikov/wallet_demo/internal/repository/mysql"
	"github.com/gchernikov/wallet_demo/internal/service"
	"github.com/gchernikov/wallet_demo/migrations"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := loadConfig()

	db, err := mysqlrepo.NewDB(mysqlrepo.Config{
		DSN:             cfg.dsn,
		MaxOpenConns:    cfg.maxOpenConns,
		MaxIdleConns:    cfg.maxIdleConns,
		ConnMaxLifetime: cfg.connMaxLifetime,
	})
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer db.Close() //nolint:errcheck

	if err = runMigrations(db); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	stmts, err := mysqlrepo.PrepareStatements(db)
	if err != nil {
		return fmt.Errorf("prepare statements: %w", err)
	}
	defer stmts.Close()

	sfuService := service.NewSelectForUpdateService(db, stmts)
	olService := service.NewOptimisticService(db, stmts, cfg.olMaxRetries)

	mux := http.NewServeMux()

	mux.HandleFunc("POST /balances/update/select-for-update", updateHandler(sfuService))
	mux.HandleFunc("POST /balances/update/optimistic", updateHandler(olService))
	mux.HandleFunc("GET /diagnostics/noop", noopHandler)
	mux.HandleFunc("GET /diagnostics/db-ping", dbPingHandler(stmts))
	mux.HandleFunc("GET /check", checkHandler)

	addr := ":" + cfg.httpPort
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Printf("listening on %s", addr)
	if err = srv.ListenAndServe(); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

func updateHandler(svc service.BalanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req model.UpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid request"}`)) //nolint:errcheck,gosec
			return
		}
		if _, err := uuid.Parse(req.WalletUUID); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"wallet_uuid must be a valid UUID"}`)) //nolint:errcheck,gosec
			return
		}
		if req.TransactionID != "" {
			if _, err := uuid.Parse(req.TransactionID); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"transaction_id must be a valid UUID"}`)) //nolint:errcheck,gosec
				return
			}
		}

		resp, err := svc.Update(r.Context(), req)
		if err != nil {
			var conflictErr *model.ConflictError
			if errors.As(err, &conflictErr) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck,gosec
				return
			}
			if errors.Is(err, sql.ErrNoRows) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":"account not found"}`)) //nolint:errcheck,gosec
				return
			}
			log.Printf("update error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"internal server error"}`)) //nolint:errcheck,gosec
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck,gosec
	}
}

func noopHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`)) //nolint:errcheck,gosec
}

func dbPingHandler(stmts *mysqlrepo.Statements) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := stmts.Ping.QueryRowContext(r.Context()).Err(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"db ping failed"}`)) //nolint:errcheck,gosec
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck,gosec
	}
}

func checkHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck,gosec
}

func runMigrations(db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("mysql"); err != nil {
		return err
	}
	return goose.Up(db, ".")
}

type config struct {
	httpPort        string
	dsn             string
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	olMaxRetries    int
}

func loadConfig() config {
	return config{
		httpPort:        getEnv("HTTP_PORT", "8080"),
		dsn:             getEnv("DB_DSN", "root:@tcp(127.0.0.1:3306)/wallet_demo?parseTime=true"),
		maxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 140),
		maxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 140),
		connMaxLifetime: getEnvDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		olMaxRetries:    getEnvInt("OL_MAX_RETRIES", 5),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
