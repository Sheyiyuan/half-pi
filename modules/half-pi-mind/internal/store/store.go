// Package store provides SQLite-backed persistence for sessions,
// devices, and audit logs.
package store

import "fmt"

// Store wraps an SQLite database.
type Store struct{}

// New opens or creates the database at the given path.
func New(path string) (*Store, error) {
	return &Store{}, nil
}

// Close shuts down the database connection.
func (s *Store) Close() error {
	return fmt.Errorf("not implemented")
}
