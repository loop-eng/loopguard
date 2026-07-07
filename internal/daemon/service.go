package daemon

import (
	"context"
	"log/slog"

	"github.com/kardianos/service"

	"github.com/loop-eng/loopguard/internal/config"
)

func ServiceConfig() *service.Config {
	return &service.Config{
		Name:        "loopguard",
		DisplayName: "LoopGuard",
		Description: "Circuit breaker daemon for AI agent loops",
		Option: service.KeyValue{
			"RunAtLoad": true,
		},
	}
}

type program struct {
	daemon *Daemon
	logger *slog.Logger
	cfg    *config.Config
	cancel context.CancelFunc
	done   chan struct{}
}

func NewProgram(logger *slog.Logger, cfg *config.Config) *program {
	return &program{logger: logger, cfg: cfg}
}

func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	p.daemon = New(ctx, p.logger, p.cfg)
	go func() {
		p.daemon.Run()
		close(p.done)
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.daemon != nil {
		p.daemon.Shutdown()
	}
	if p.done != nil {
		<-p.done
	}
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

func InstallService(logger *slog.Logger, cfg *config.Config) error {
	prg := NewProgram(logger, cfg)
	svc, err := service.New(prg, ServiceConfig())
	if err != nil {
		return err
	}
	return svc.Install()
}

func UninstallService(logger *slog.Logger, cfg *config.Config) error {
	prg := NewProgram(logger, cfg)
	svc, err := service.New(prg, ServiceConfig())
	if err != nil {
		return err
	}
	return svc.Uninstall()
}

func StartService(logger *slog.Logger, cfg *config.Config) error {
	prg := NewProgram(logger, cfg)
	svc, err := service.New(prg, ServiceConfig())
	if err != nil {
		return err
	}
	return svc.Start()
}

func StopService(logger *slog.Logger, cfg *config.Config) error {
	prg := NewProgram(logger, cfg)
	svc, err := service.New(prg, ServiceConfig())
	if err != nil {
		return err
	}
	return svc.Stop()
}

func RunAsService(logger *slog.Logger, cfg *config.Config) error {
	prg := NewProgram(logger, cfg)
	svc, err := service.New(prg, ServiceConfig())
	if err != nil {
		return err
	}
	return svc.Run()
}

func IsInstalled() bool {
	prg := &program{}
	svc, err := service.New(prg, ServiceConfig())
	if err != nil {
		return false
	}
	status, err := svc.Status()
	return err == nil && status != service.StatusUnknown
}
