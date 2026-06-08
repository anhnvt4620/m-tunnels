package service

import (
	"context"
	"log/slog"

	"github.com/kardianos/service"
)

type RunFunc func(ctx context.Context) error

type Program struct {
	run    RunFunc
	log    *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
}

func NewProgram(run RunFunc, log *slog.Logger) *Program {
	return &Program{run: run, log: log, done: make(chan struct{})}
}

func (p *Program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() {
		defer close(p.done)
		if err := p.run(ctx); err != nil {
			p.log.Error("service run error", "err", err)
		}
	}()
	return nil
}

func (p *Program) Stop(s service.Service) error {
	p.cancel()
	<-p.done
	return nil
}

func ServiceConfig(name, displayName, description string) *service.Config {
	return &service.Config{
		Name:        name,
		DisplayName: displayName,
		Description: description,
	}
}
