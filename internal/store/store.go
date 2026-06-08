package store

import "errors"

var ErrNotFound = errors.New("not found")

type Client struct {
	ID          string
	Token       string
	PublicPort  int
	AllowedIPs  []string
	DisplayName string
	CreatedAt   int64
	UpdatedAt   int64
}

type Store interface {
	CountClients() (int, error)
	ListClients() ([]Client, error)
	GetClient(id string) (*Client, error)
	CreateClient(c *Client) error
	UpdateClient(c *Client) error
	DeleteClient(id string) error
	AllocatePort(start, end int) (int, error)
	ReservePort(port int) error
	FreePort(port int) error
	Close() error
}
