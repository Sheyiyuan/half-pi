// Package store provides SQLite-backed persistence for sessions,
// devices, and audit logs.
package store

// Store wraps an SQLite database.
type Store struct{}

// New opens or creates the database at the given path.
func New(path string) (*Store, error) {
	return &Store{}, nil
}

// Close 关闭数据库连接。
// TODO: 接入 SQLite 后实现真正的关闭逻辑。
func (s *Store) Close() error {
	return nil
}
