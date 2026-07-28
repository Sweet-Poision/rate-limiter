package ratelimiter

import (
	"database/sql"
	"log"
	"os"

	_ "github.com/lib/pq"
)

var db *sql.DB

func LoadEndpointsFromDB() {
	var expectedCount int
	err := db.QueryRow("SELECT COUNT(*) FROM endpoints").Scan(&expectedCount)

	if err != nil {
		log.Fatalf("Failed to execute pre-flight count query: %v", err)
	}

	shadowMap := make(map[string]endpointData)

	query := "SELECT path, refil_wait_time_ms, max_limit FROM endpoints"
	rows, err := db.Query(query)

	if err != nil {
		log.Fatalf("Failed to query endpoints on startup: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		var data endpointData

		err := rows.Scan(&path, &data.refilWaitTimeMS, &data.maxLimitBucket)
		if err != nil {
			log.Printf("Failed to parse database row: %v", err)
			continue
		}

		shadowMap[path] = data
	}
	if err = rows.Err(); err != nil {
		log.Fatalf("Network error during row iteration: %v", err)
	}

	if len(shadowMap) != expectedCount {
		log.Fatalf("Data mismatch: expected %d rows, but loaded %d endpoints", expectedCount, len(shadowMap))
	}

	availableEndpoints.mu.Lock()
	availableEndpoints.endpoints = shadowMap
	availableEndpoints.mu.Unlock()
}

func InitDB() {
	connStr := os.Getenv("DB_URL")
	if connStr == "" {
		log.Fatal("DB_URL environment variable is not set")
	}

	var err error
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Failed to initialize databse pool: ", err)
	}

	if err = db.Ping(); err != nil {
		log.Fatal("Database is unreachable: ", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

}
