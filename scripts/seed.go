package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	// Load environment variables from .env file
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

	err = db.Ping()
	if err != nil {
		log.Fatal("Failed to ping DB: ", err)
	}

	migrationSQL, err := os.ReadFile("migrations/001_create_endpoints_table.sql")
	if err != nil {
		log.Fatal("Failed to read migration file: ", err)
	}

	_, err = db.Exec(string(migrationSQL))
	if err != nil {
		log.Fatal("Failed to execute migration: ", err)
	}

	insertSQL := `
		INSERT INTO endpoints (path,refil_wait_time_ms, max_limit)
		VALUES
	 		('/api/v1/health', 100, 5),
			('/api/v1/data', 200, 10),
			('/api/v1/payment', 500, 8),
			('/api/v1/health2', 100, 3),
			('/api/v1/health3', 300, 100),
			('/api/v1/health4', 170, 6)
		ON CONFLICT (path) DO UPDATE
		SET refil_wait_time_ms = EXCLUDED.refil_wait_time_ms, max_limit = EXCLUDED.max_limit;
	`

	_, err = db.Exec(insertSQL)
	if err != nil {
		log.Fatal("Failed to insert seed data: ", err)
	}
	fmt.Println("Database migration and seeding completed")
}
