package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"m-tunnel/internal/config"
	"m-tunnel/internal/dashboard"
	"m-tunnel/internal/gateway"
	"m-tunnel/internal/logger"
	"m-tunnel/internal/store"
)

func main() {
	configPath := flag.String("config", "configs/gateway.yaml", "path to gateway config YAML")
	logFile := flag.String("log-file", "", "optional log file path")
	flag.Parse()

	log := logger.Setup(*logFile)
	slog.SetDefault(log)

	cfg, err := config.LoadGateway(*configPath)
	if err != nil {
		log.Error("load gateway config failed", "err", err)
		os.Exit(1)
	}

	st, err := store.NewSQLiteStore(cfg.Database.Path)
	if err != nil {
		log.Error("open sqlite store failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := seedFromConfigIfEmpty(cfg, st, log); err != nil {
		log.Error("seed store from config failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	gw := gateway.New(cfg, st, log)
	if cfg.Dashboard.Enabled {
		dash := dashboard.New(gw, st, cfg, log)
		go func() {
			if err := dash.Run(ctx); err != nil {
				log.Error("dashboard stopped with error", "err", err)
				stop()
			}
		}()
	}

	log.Info("gateway started")
	if err := gw.Run(ctx); err != nil {
		log.Error("gateway stopped with error", "err", err)
		os.Exit(1)
	}
}

func seedFromConfigIfEmpty(cfg *config.GatewayConfig, st store.Store, log *slog.Logger) error {
	count, err := st.CountClients()
	if err != nil {
		return err
	}
	if count > 0 || len(cfg.Clients) == 0 {
		return nil
	}

	for _, c := range cfg.Clients {
		port := c.PublicPort
		if port == 0 {
			port, err = st.AllocatePort(cfg.PortRange.Start, cfg.PortRange.End)
			if err != nil {
				return err
			}
		} else {
			if err := st.ReservePort(port); err != nil {
				return err
			}
		}
		if err := st.CreateClient(&store.Client{
			ID:          c.ClientID,
			Token:       c.Token,
			PublicPort:  port,
			AllowedIPs:  c.AllowedIPs,
			DisplayName: c.DisplayName,
		}); err != nil {
			return err
		}
		log.Info("seeded client into sqlite store", "client_id", c.ClientID, "port", port)
	}
	return nil
}
