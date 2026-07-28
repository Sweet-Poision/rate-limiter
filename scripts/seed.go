package main

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	_ = godotenv.Load()

	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://admin:secret@localhost:5432/gateway?sslmode=disable"
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Failed to open DB connection: ", err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatal("Failed to ping DB: ", err)
	}

	migrationSQL, err := os.ReadFile("migrations/001_create_endpoints_table.sql")
	if err == nil {
		if _, err = db.Exec(string(migrationSQL)); err != nil {
			log.Fatal("Failed to execute migration: ", err)
		}
	}

	// Generate realistic API paths
	versions := []string{"v1", "v2"}
	services := []string{"auth", "users", "billing", "models", "datasets", "agents", "rag", "payments", "analytics", "reports"}
	resources := []string{"profile", "settings", "metrics", "logs", "config", "keys", "status", "health", "cache", "sync"}
	actions := []string{"create", "read", "update", "delete", "list"}

	var valueStrings []string
	var valueArgs []interface{}
	argID := 1

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for _, v := range versions {
		for _, svc := range services {
			for _, res := range resources {
				for _, act := range actions {
					path := fmt.Sprintf("/api/%s/%s/%s/%s", v, svc, res, act)

					var maxLimit, refillWaitMS int

					// Determine limits based on computational cost
					if svc == "models" || svc == "agents" || svc == "rag" {
						// Expensive backend workflows get strict limits
						maxLimit = 5
						refillWaitMS = 5000
					} else if res == "health" || res == "status" {
						// Health checks get high throughput
						maxLimit = 1000
						refillWaitMS = 100
					} else {
						// Standard endpoints get varied but moderate limits
						maxLimit = 10 + rng.Intn(90)
						refillWaitMS = 500 + rng.Intn(1500)
					}

					valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d)", argID, argID+1, argID+2))
					valueArgs = append(valueArgs, path, refillWaitMS, maxLimit)
					argID += 3
				}
			}
		}
	}

	// Construct parameterized bulk insert query
	query := fmt.Sprintf(`
		INSERT INTO endpoints (path, refill_wait_time_ms, max_limit)
		VALUES %s
		ON CONFLICT (path) DO UPDATE
		SET refill_wait_time_ms = EXCLUDED.refill_wait_time_ms,
		    max_limit = EXCLUDED.max_limit;
	`, strings.Join(valueStrings, ","))

	// PostgreSQL limit is 65535 parameters per query.
	// We are generating 1000 rows * 3 params = 3000 params, which is safe.
	_, err = db.Exec(query, valueArgs...)
	if err != nil {
		log.Fatal("Failed to bulk insert seed data: ", err)
	}

	fmt.Printf("Database migration and seeding completed. Inserted %d endpoints.\n", len(valueStrings))
}
