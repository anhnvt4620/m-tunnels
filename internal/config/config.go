package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type GatewayConfig struct {
	Server struct {
		AgentListenAddr string `yaml:"agent_listen_addr"`
		AgentPath       string `yaml:"agent_path"`
		PublicBindAddr  string `yaml:"public_bind_addr"`
	} `yaml:"server"`
	Security struct {
		MinTokenLength int `yaml:"min_token_length"`
	} `yaml:"security"`
	PortRange struct {
		Start int `yaml:"start"`
		End   int `yaml:"end"`
	} `yaml:"port_range"`
	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`
	Dashboard DashboardConfig `yaml:"dashboard"`
	// Clients là danh sách seed legacy để migrate vào DB lần đầu.
	// Sau khi đã có data trong DB thì không dùng nữa.
	Clients []GatewayClient `yaml:"clients"`
}

type DashboardConfig struct {
	Enabled       bool     `yaml:"enabled"`
	ListenAddr    string   `yaml:"listen_addr"`
	BasicAuthUser string   `yaml:"basic_auth_user"`
	BasicAuthHash string   `yaml:"basic_auth_hash"` // bcrypt hash
	AllowedIPs    []string `yaml:"allowed_ips"`
}

type GatewayClient struct {
	ClientID    string   `yaml:"client_id"`
	Token       string   `yaml:"token"`
	PublicPort  int      `yaml:"public_port"`
	AllowedIPs  []string `yaml:"allowed_ips"`
	DisplayName string   `yaml:"display_name"`
}

type AgentConfig struct {
	Agent struct {
		ClientID   string `yaml:"client_id"`
		Token      string `yaml:"token"`
		GatewayURL string `yaml:"gateway_url"`
		LocalAddr  string `yaml:"local_addr"`
		Reconnect  struct {
			MinSeconds int `yaml:"min_seconds"`
			MaxSeconds int `yaml:"max_seconds"`
		} `yaml:"reconnect"`
		Service struct {
			Name        string `yaml:"name"`
			DisplayName string `yaml:"display_name"`
			Description string `yaml:"description"`
		} `yaml:"service"`
	} `yaml:"agent"`
}

func LoadGateway(path string) (*GatewayConfig, error) {
	var cfg GatewayConfig
	if err := loadYAML(path, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadAgent(path string) (*AgentConfig, error) {
	var cfg AgentConfig
	if err := loadYAML(path, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadYAML(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func (c *GatewayConfig) Validate() error {
	if c.Server.AgentListenAddr == "" {
		return errors.New("server.agent_listen_addr is required")
	}
	if c.Server.AgentPath == "" {
		return errors.New("server.agent_path is required")
	}
	if c.Server.PublicBindAddr == "" {
		c.Server.PublicBindAddr = "0.0.0.0"
	}
	if c.Security.MinTokenLength == 0 {
		c.Security.MinTokenLength = 32
	}
	if c.PortRange.Start == 0 {
		c.PortRange.Start = 12600
	}
	if c.PortRange.End == 0 {
		c.PortRange.End = 12799
	}
	if c.PortRange.Start >= c.PortRange.End {
		return errors.New("port_range.start must be less than port_range.end")
	}
	if c.Database.Path == "" {
		c.Database.Path = "gateway.db"
	}
	if c.Dashboard.Enabled {
		if c.Dashboard.ListenAddr == "" {
			c.Dashboard.ListenAddr = "127.0.0.1:8080"
		}
		if c.Dashboard.BasicAuthUser == "" {
			return errors.New("dashboard.basic_auth_user is required when dashboard is enabled")
		}
		if c.Dashboard.BasicAuthHash == "" {
			return errors.New("dashboard.basic_auth_hash is required when dashboard is enabled")
		}
		for _, ip := range c.Dashboard.AllowedIPs {
			if net.ParseIP(ip) == nil {
				return fmt.Errorf("dashboard.allowed_ips contains invalid IP %q", ip)
			}
		}
	}
	// Validate seed clients nếu có
	for _, client := range c.Clients {
		if client.ClientID == "" {
			return errors.New("seed client_id is required")
		}
		if len(client.Token) < c.Security.MinTokenLength {
			return fmt.Errorf("seed client %s token must be at least %d characters", client.ClientID, c.Security.MinTokenLength)
		}
		if client.PublicPort != 0 {
			if client.PublicPort < 1 || client.PublicPort > 65535 {
				return fmt.Errorf("seed client %s public_port is invalid", client.ClientID)
			}
		}
		for _, ip := range client.AllowedIPs {
			if net.ParseIP(ip) == nil {
				return fmt.Errorf("seed client %s allowed_ips contains invalid IP %q", client.ClientID, ip)
			}
		}
	}
	return nil
}

func (c *AgentConfig) Validate() error {
	if c.Agent.ClientID == "" {
		return errors.New("agent.client_id is required")
	}
	if len(c.Agent.Token) < 32 {
		return errors.New("agent.token must be at least 32 characters")
	}
	if c.Agent.GatewayURL == "" {
		return errors.New("agent.gateway_url is required")
	}
	if c.Agent.LocalAddr == "" {
		return errors.New("agent.local_addr is required")
	}
	if c.Agent.Reconnect.MinSeconds <= 0 {
		c.Agent.Reconnect.MinSeconds = 5
	}
	if c.Agent.Reconnect.MaxSeconds <= 0 {
		c.Agent.Reconnect.MaxSeconds = 30
	}
	if c.Agent.Reconnect.MaxSeconds < c.Agent.Reconnect.MinSeconds {
		return errors.New("agent.reconnect.max_seconds must be >= min_seconds")
	}
	if c.Agent.Service.Name == "" {
		c.Agent.Service.Name = "M-Tunnel Agent"
	}
	if c.Agent.Service.DisplayName == "" {
		c.Agent.Service.DisplayName = "M-Tunnel SQL Tunnel Agent"
	}
	if c.Agent.Service.Description == "" {
		c.Agent.Service.Description = "Reverse tunnel agent for SQL Server"
	}
	return nil
}

func (c *AgentConfig) MinReconnectDelay() time.Duration {
	return time.Duration(c.Agent.Reconnect.MinSeconds) * time.Second
}

func (c *AgentConfig) MaxReconnectDelay() time.Duration {
	return time.Duration(c.Agent.Reconnect.MaxSeconds) * time.Second
}
