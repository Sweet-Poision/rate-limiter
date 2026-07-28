package repository

import (
	"database/sql"
	"ratelimiter/internal/domain"

	_ "github.com/lib/pq"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(dbURL string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	return &PostgresStore{db: db}, nil
}

func (p *PostgresStore) LoadAllEndpoints() (map[string]domain.EndpointData, error) {
	query := "SELECT path, refill_wait_time_ms, max_limit FROM endpoints"
	rows, err := p.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	endpoints := make(map[string]domain.EndpointData)
	for rows.Next() {
		var data domain.EndpointData
		err := rows.Scan(&data.Path, &data.RefillWaitTimeMS, &data.MaxLimit)
		if err != nil {
			continue
		}
		endpoints[data.Path] = data
	}

	return endpoints, rows.Err()
}

func (p *PostgresStore) GetEndpointByPath(path string) (domain.EndpointData, error) {
	var data domain.EndpointData
	query := "SELECT path, refill_wait_time_ms, max_limit FROM endpoints WHERE path = $1"
	err := p.db.QueryRow(query, path).Scan(&data.Path, &data.RefillWaitTimeMS, &data.MaxLimit)
	return data, err
}
