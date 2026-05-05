package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/go-logr/logr"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"

	"go.minekube.com/gate/pkg/edition/java/lite/config"
	"go.minekube.com/gate/pkg/internal/api/gen/minekube/gate/v1/gatev1connect"
)

func NewServer(cfg Config, h Handler, liteConfig *config.Config) *Server {
	return &Server{
		cfg:        cfg,
		h:          h,
		liteConfig: liteConfig,
	}
}

type Server struct {
	cfg        Config
	h          Handler
	liteConfig *config.Config
}

func (s *Server) Start(ctx context.Context) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("starting api service", "bind", s.cfg.Bind)

	otelInterceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return err
	}

	mux := http.NewServeMux()

	// Mount ConnectRPC handler (existing)
	mux.Handle(gatev1connect.NewGateServiceHandler(s.h, connect.WithInterceptors(otelInterceptor)))

	// Mount Lite REST handler (new) – only if lite mode config is available
	if s.liteConfig != nil {
		lr := NewLiteRouter(s.liteConfig)
		path, handler := lr.Handler()
		mux.Handle(path, handler)
		log.Info("lite API endpoints enabled", "prefix", path)
	}

	hs := &http.Server{
		Addr: s.cfg.Bind,
		Handler: h2c.NewHandler(mux, &http2.Server{
			IdleTimeout: time.Second * 30,
		}),
		ReadTimeout:       time.Second * 5,
		ReadHeaderTimeout: time.Second * 5,
		WriteTimeout:      time.Second * 10,
		IdleTimeout:       time.Second * 30,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		return hs.Shutdown(stopCtx)
	})
	eg.Go(func() error { return ignoreClosed(hs.ListenAndServe()) })

	return eg.Wait()
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
