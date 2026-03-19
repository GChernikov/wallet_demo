// cmd/seed generates load test wallets and writes UUID lists for k6.
// Run after migrations: go run ./cmd/seed
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	natsrepo "github.com/gchernikov/wallet_demo/internal/repository/nats"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dsn := getEnv("DB_DSN", "root@tcp(localhost:3306)/wallet_demo?parseTime=true")

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close() //nolint:errcheck

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err = db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	// Scenario A: 750 wallets × 4 RPS = 3000 RPS (low contention)
	log.Println("seeding 750 wallets for 4 RPS/wallet scenario (SFU)...")
	sfuIDs750 := generateAndInsert(db, 750)
	writeJSON("loadtest/wallets-sfu-750.json", sfuIDs750)

	log.Println("seeding 750 wallets for 4 RPS/wallet scenario (optimistic)...")
	olIDs750 := generateAndInsert(db, 750)
	writeJSON("loadtest/wallets-ol-750.json", olIDs750)

	// Scenario B: 100 wallets × 30 RPS = 3000 RPS (high contention)
	log.Println("seeding 100 wallets for 30 RPS/wallet scenario (SFU)...")
	sfuIDs100 := generateAndInsert(db, 100)
	writeJSON("loadtest/wallets-sfu-100.json", sfuIDs100)

	log.Println("seeding 100 wallets for 30 RPS/wallet scenario (optimistic)...")
	olIDs100 := generateAndInsert(db, 100)
	writeJSON("loadtest/wallets-ol-100.json", olIDs100)

	// NATS seeding — only if NATS_URL is set.
	if natsURL := getEnv("NATS_URL", ""); natsURL != "" {
		nc, js, err := natsrepo.Connect(natsURL)
		if err != nil {
			return fmt.Errorf("connect to nats: %w", err)
		}
		defer nc.Drain() //nolint:errcheck

		log.Println("seeding 750 wallets into NATS...")
		natsIDs750 := generateAndPublish(js, 750)
		writeJSON("loadtest/wallets-nats-750.json", natsIDs750)

		log.Println("seeding 100 wallets into NATS...")
		natsIDs100 := generateAndPublish(js, 100)
		writeJSON("loadtest/wallets-nats-100.json", natsIDs100)
	}

	log.Println("seed complete")
	return nil
}

// generateAndPublish creates count wallets with random UUIDs, seeds them into NATS, and returns IDs.
func generateAndPublish(js jetstream.JetStream, count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = uuid.New().String()
	}

	seedPayload, _ := json.Marshal(natsrepo.WalletEvent{ //nolint:errchkjson
		Amount:        1_000_000,
		NewBalance:    1_000_000,
		TransactionID: "seed",
		EventType:     "credit",
	})

	for _, id := range ids {
		_, err := js.Publish(context.Background(), "wallets."+id, seedPayload,
			jetstream.WithExpectLastSequencePerSubject(0))
		if err != nil {
			log.Printf("warn: already seeded %s, skipping", id)
		}
	}
	return ids
}

// generateAndInsert creates count wallets with random UUIDs, inserts them, and returns the UUID strings.
func generateAndInsert(db *sql.DB, count int) []string {
	const (
		batchSize    = 500
		startBalance = int64(1_000_000)
	)
	ids := make([]string, count)

	for i := range ids {
		ids[i] = uuid.New().String()
	}

	for start := 0; start < count; start += batchSize {
		end := min(start+batchSize, count)

		var sb strings.Builder
		sb.WriteString("INSERT IGNORE INTO balances (wallet_uuid, balance) VALUES ")
		args := make([]any, 0, (end-start)*2)

		for i, id := range ids[start:end] {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(?, ?)")
			parsed := uuid.MustParse(id)
			args = append(args, parsed[:], startBalance)
		}

		if _, err := db.ExecContext(context.Background(), sb.String(), args...); err != nil {
			log.Fatalf("insert batch: %v", err)
		}
	}

	return ids
}

func writeJSON(path string, ids []string) {
	data, err := json.Marshal(ids)
	if err != nil {
		log.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}
	log.Printf("wrote %s (%d wallets)", path, len(ids))
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
