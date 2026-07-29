package limiter

import (
	"database/sql"
	"testing"

	"ratelimiter/internal/domain"
	"ratelimiter/internal/repository"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newTestRegistryWithMockDB(t *testing.T) (*Registry, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	store := repository.NewPostgresStoreWithDB(db)
	registry := NewRegistry(store, make(map[string]domain.EndpointData))

	cleanup := func() { db.Close() }
	return registry, mock, cleanup
}

func TestGetEndpoint_DBMissFallsThroughToNegativeCache(t *testing.T) {
	registry, mock, cleanup := newTestRegistryWithMockDB(t)
	defer cleanup()

	path := "/api/v1/does-not-exist"

	// Expect exactly ONE query to hit the DB for this path.
	mock.ExpectQuery("SELECT path, refill_wait_time_ms, max_limit FROM endpoints WHERE path = \\$1").
		WithArgs(path).
		WillReturnError(sql.ErrNoRows)

	_, err := registry.GetEndpoint(path)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows on first (real) miss, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("first call: unmet DB expectations: %v", err)
	}
}

func TestGetEndpoint_RepeatedMissesDoNotHitDBAgain(t *testing.T) {
	registry, mock, cleanup := newTestRegistryWithMockDB(t)
	defer cleanup()

	path := "/api/v1/does-not-exist"

	// The mock is set up to allow exactly ONE query. If the negative cache
	// isn't working, the second/third GetEndpoint call below would attempt
	// a second query against a mock with no matching expectation left,
	// which sqlmock treats as a hard failure — so this test fails loudly
	// and specifically if negative caching regresses, rather than silently
	// passing regardless of whether caching actually happened.
	mock.ExpectQuery("SELECT path, refill_wait_time_ms, max_limit FROM endpoints WHERE path = \\$1").
		WithArgs(path).
		WillReturnError(sql.ErrNoRows)

	// First call: real DB miss, populates the negative cache.
	if _, err := registry.GetEndpoint(path); err != sql.ErrNoRows {
		t.Fatalf("call 1: expected sql.ErrNoRows, got %v", err)
	}

	// Second and third calls: should be served entirely from the negative
	// cache. No additional query expectation was registered above, so if
	// the code path incorrectly falls through to the DB again, sqlmock
	// will report an unexpected query and this test fails.
	for i := 2; i <= 3; i++ {
		if _, err := registry.GetEndpoint(path); err != sql.ErrNoRows {
			t.Fatalf("call %d: expected sql.ErrNoRows from negative cache, got %v", i, err)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected exactly one DB query across three lookups, but expectations were not met as expected: %v", err)
	}
}

func TestGetEndpoint_PositiveLookupSucceedsAndCaches(t *testing.T) {
	registry, mock, cleanup := newTestRegistryWithMockDB(t)
	defer cleanup()

	path := "/api/v1/health1"
	rows := sqlmock.NewRows([]string{"path", "refill_wait_time_ms", "max_limit"}).
		AddRow(path, 100, 5)

	// Expect exactly one query — a second lookup for the same path should
	// be served from the positive in-memory cache, not the DB again.
	mock.ExpectQuery("SELECT path, refill_wait_time_ms, max_limit FROM endpoints WHERE path = \\$1").
		WithArgs(path).
		WillReturnRows(rows)

	data, err := registry.GetEndpoint(path)
	if err != nil {
		t.Fatalf("expected successful lookup, got error: %v", err)
	}
	if data.MaxLimit != 5 || data.RefillWaitTimeMS != 100 {
		t.Fatalf("unexpected endpoint data: %+v", data)
	}

	// Second call — must NOT trigger a second DB query.
	if _, err := registry.GetEndpoint(path); err != nil {
		t.Fatalf("second lookup (should be cached): unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected exactly one DB query for two lookups of the same known endpoint: %v", err)
	}
}
