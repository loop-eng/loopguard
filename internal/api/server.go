package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

type Server struct {
	logger  *slog.Logger
	mux     *http.ServeMux
	httpSrv *http.Server
	backend DaemonBackend
}

func New(logger *slog.Logger) *Server {
	return newServer(logger, nil)
}

func NewWithDaemon(logger *slog.Logger, backend DaemonBackend) *Server {
	return newServer(logger, backend)
}

func newServer(logger *slog.Logger, backend DaemonBackend) *Server {
	mux := http.NewServeMux()
	s := &Server{
		logger:  logger,
		mux:     mux,
		backend: backend,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/sessions", s.handleSessions)
	s.mux.HandleFunc("POST /api/sessions/{id}/resume", s.handleResume)
}

func (s *Server) Serve(ctx context.Context, socketPath string) error {
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale socket: %w", err)
	}
	oldMask := syscall.Umask(0177)
	listener, err := net.Listen("unix", socketPath)
	syscall.Umask(oldMask)
	if err != nil {
		return err
	}
	s.logger.Info("API server listening", "socket", socketPath)

	s.httpSrv = &http.Server{Handler: s.mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("server shutdown error", "error", err)
		}
	}()

	if err := s.httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func SocketPath() string {
	home, _ := os.UserHomeDir()
	return home + "/.config/loopguard/loopguard.sock"
}

func IsDaemonRunning() bool {
	conn, err := net.Dial("unix", SocketPath())
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
