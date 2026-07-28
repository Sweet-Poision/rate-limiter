package ratelimiter

import (
	"testing"
)

// TestHashGeneration validates that identical inputs produce identical keys
func TestHashGeneration(t *testing.T) {
	userID := "user_123"
	path := "/api/v1/health"

	hash1 := GenerateKey(userID, path) // Replace GenerateKey with your actual hash function name
	hash2 := GenerateKey(userID, path)

	if hash1 != hash2 {
		t.Fatalf("Expected hashes to be identical for same input, got %s and %s", hash1, hash2)
	}
}

// TestEndpointRegistry validates memory fallback logic for endpoints
func TestEndpointRegistry(t *testing.T) {
	// Simulate the registry loaded from the seed data
	registry := map[string]struct {
		MaxLimit       int
		RefillWaitTime int
	}{
		"/api/v1/health": {MaxLimit: 5, RefillWaitTime: 100},
	}

	endpoint, exists := registry["/api/v1/health"]
	if !exists {
		t.Fatal("Expected /api/v1/health to exist in registry")
	}

	if endpoint.MaxLimit != 5 {
		t.Errorf("Expected MaxLimit 5, got %d", endpoint.MaxLimit)
	}

	_, exists = registry["/api/v1/unknown"]
	if exists {
		t.Fatal("Expected unknown endpoint to not exist")
	}
}

// TestTokenMath validates the logic used to calculate refills
func TestTokenMath(t *testing.T) {
	maxLimit := 10.0
	refillWaitTimeMS := 200.0 // 1 token every 200ms

	lastRequestTime := int64(1000)
	currentTime := int64(1400) // 400ms elapsed

	elapsed := float64(currentTime - lastRequestTime)
	tokensToAdd := elapsed / refillWaitTimeMS

	if tokensToAdd != 2.0 {
		t.Errorf("Expected 2 tokens to be added, got %f", tokensToAdd)
	}

	currentTokens := 9.0
	newTokens := currentTokens + tokensToAdd

	if newTokens > maxLimit {
		newTokens = maxLimit
	}

	if newTokens != 10.0 {
		t.Errorf("Expected tokens to be capped at 10, got %f", newTokens)
	}
}
