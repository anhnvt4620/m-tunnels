package dashboard

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"m-tunnel/internal/config"
	"m-tunnel/internal/gateway"
	"m-tunnel/internal/store"
)

type Server struct {
	gw    *gateway.Gateway
	store store.Store
	cfg   config.DashboardConfig
	pcfg  config.GatewayConfig
	log   *slog.Logger
}

func New(gw *gateway.Gateway, st store.Store, cfg *config.GatewayConfig, log *slog.Logger) *Server {
	return &Server{
		gw:    gw,
		store: st,
		cfg:   cfg.Dashboard,
		pcfg:  *cfg,
		log:   log,
	}
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	server := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.middleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("dashboard listening", "addr", s.cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.checkIP(r) {
			s.log.Warn("dashboard IP rejected", "remote", r.RemoteAddr, "path", r.URL.Path)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !s.checkBasicAuth(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="M-Tunnel Dashboard"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) checkIP(r *http.Request) bool {
	if len(s.cfg.AllowedIPs) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.TrimSpace(host)
	for _, ip := range s.cfg.AllowedIPs {
		if ip == host {
			return true
		}
	}
	return false
}

func (s *Server) checkBasicAuth(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.BasicAuthUser)) != 1 {
		return false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(s.cfg.BasicAuthHash), []byte(pass)); err != nil {
		return false
	}
	return true
}
