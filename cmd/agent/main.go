package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	kservice "github.com/kardianos/service"

	"m-tunnel/internal/agent"
	"m-tunnel/internal/config"
	"m-tunnel/internal/logger"
	"m-tunnel/internal/service"
)

func main() {
	configPath := flag.String("config", "configs/agent.yaml", "path to agent config YAML")
	serviceAction := flag.String("service", "", "service action: install, uninstall, start, stop, restart")
	logFile := flag.String("log-file", defaultLogFile(), "optional log file path")
	flag.Parse()

	log := logger.Setup(*logFile)
	slog.SetDefault(log)

	cfg, err := config.LoadAgent(*configPath)
	if err != nil {
		log.Error("load agent config failed", "err", err)
		os.Exit(1)
	}

	runner := agent.New(cfg, log)
	program := service.NewProgram(runner.Run, log)
	svcConfig := service.ServiceConfig(cfg.Agent.Service.Name, cfg.Agent.Service.DisplayName, cfg.Agent.Service.Description)
	svcConfig.Arguments = []string{"-config", *configPath, "-log-file", *logFile}

	svc, err := kservice.New(program, svcConfig)
	if err != nil {
		log.Error("create service failed", "err", err)
		os.Exit(1)
	}

	if *serviceAction != "" {
		if err := kservice.Control(svc, *serviceAction); err != nil {
			log.Error("service action failed", "action", *serviceAction, "err", err)
			os.Exit(1)
		}
		log.Info("service action completed", "action", *serviceAction)
		return
	}

	if kservice.Interactive() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runner.Run(ctx); err != nil {
			log.Error("agent stopped with error", "err", err)
			os.Exit(1)
		}
		return
	}

	if err := svc.Run(); err != nil {
		log.Error("service stopped with error", "err", err)
		os.Exit(1)
	}
}

func defaultLogFile() string {
	if dir := os.Getenv("ProgramData"); dir != "" {
		return filepath.Join(dir, "M-Tunnel", "agent.log")
	}
	return ""
}
