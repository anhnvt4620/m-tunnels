package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS clients (
			id TEXT PRIMARY KEY,
			token TEXT NOT NULL,
			public_port INTEGER UNIQUE NOT NULL,
			allowed_ips TEXT NOT NULL DEFAULT '[]',
			display_name TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS allocated_ports (
			port INTEGER PRIMARY KEY
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate sqlite: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) CountClients() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *SQLiteStore) ListClients() ([]Client, error) {
	rows, err := s.db.Query(`SELECT id, token, public_port, allowed_ips, display_name, created_at, updated_at FROM clients ORDER BY public_port ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []Client
	for rows.Next() {
		c, err := scanClient(rows.Scan)
		if err != nil {
			return nil, err
		}
		clients = append(clients, c)
	}
	return clients, rows.Err()
}

func (s *SQLiteStore) GetClient(id string) (*Client, error) {
	row := s.db.QueryRow(`SELECT id, token, public_port, allowed_ips, display_name, created_at, updated_at FROM clients WHERE id = ?`, id)
	c, err := scanClient(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (s *SQLiteStore) CreateClient(c *Client) error {
	allowed, err := json.Marshal(c.AllowedIPs)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	if c.CreatedAt == 0 {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if _, err := s.db.Exec(`INSERT INTO clients (id, token, public_port, allowed_ips, display_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Token, c.PublicPort, string(allowed), c.DisplayName, c.CreatedAt, c.UpdatedAt); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) UpdateClient(c *Client) error {
	allowed, err := json.Marshal(c.AllowedIPs)
	if err != nil {
		return err
	}
	c.UpdatedAt = time.Now().Unix()
	res, err := s.db.Exec(`UPDATE clients SET token = ?, allowed_ips = ?, display_name = ?, updated_at = ? WHERE id = ?`,
		c.Token, string(allowed), c.DisplayName, c.UpdatedAt, c.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) DeleteClient(id string) error {
	res, err := s.db.Exec(`DELETE FROM clients WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) AllocatePort(start, end int) (int, error) {
	for port := start; port <= end; port++ {
		res, err := s.db.Exec(`INSERT OR IGNORE INTO allocated_ports (port) VALUES (?)`, port)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n == 1 {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free ports available in range %d-%d", start, end)
}

func (s *SQLiteStore) ReservePort(port int) error {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO allocated_ports (port) VALUES (?)`, port)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("port %d is already reserved", port)
	}
	return nil
}

func (s *SQLiteStore) FreePort(port int) error {
	_, err := s.db.Exec(`DELETE FROM allocated_ports WHERE port = ?`, port)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func scanClient(scan func(dest ...any) error) (Client, error) {
	var c Client
	var allowed string
	if err := scan(&c.ID, &c.Token, &c.PublicPort, &allowed, &c.DisplayName, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Client{}, err
	}
	if allowed == "" {
		allowed = "[]"
	}
	if err := json.Unmarshal([]byte(allowed), &c.AllowedIPs); err != nil {
		return Client{}, fmt.Errorf("parse allowed_ips for client %s: %w", c.ID, err)
	}
	return c, nil
}
