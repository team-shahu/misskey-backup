package health

import (
	"context"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

type Server struct {
	srv *http.Server
}

// NewServer addrで待ち受けるヘルスサーバ生成
func NewServer(addr string) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Start goroutineで待ち受け開始
func (s *Server) Start() {
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Errorf("Health server error: %v", err)
		}
	}()
	logrus.Infof("Health server listening on %s", s.srv.Addr)
}

// Stop グレースフルシャットダウン
func (s *Server) Stop(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
