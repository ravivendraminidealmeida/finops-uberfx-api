package fxapp

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"junglegaming.io/finops-api/internal/adapter/auth"
	httpapi "junglegaming.io/finops-api/internal/adapter/http"
	"junglegaming.io/finops-api/internal/adapter/postgres"
	sqsadapter "junglegaming.io/finops-api/internal/adapter/sqs"
	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/platform/config"
	"junglegaming.io/finops-api/internal/platform/health"
)

func Module() fx.Option {
	return fx.Options(
		configModule,
		postgresModule,
		sqsModule,
		authModule,
		appModule,
		httpModule,
		workersModule,
	)
}

var configModule = fx.Module("config",
	fx.Provide(config.Load),
	fx.Provide(func() (*zap.Logger, error) { return zap.NewProduction() }),
)

var postgresModule = fx.Module("postgres",
	fx.Provide(func(lc fx.Lifecycle, cfg config.Config) (*pgxpool.Pool, error) {
		if err := postgres.RunMigrations(cfg.DatabaseURL); err != nil {
			return nil, err
		}
		pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
		if err != nil {
			return nil, err
		}
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error { return pool.Ping(ctx) },
			OnStop:  func(ctx context.Context) error { pool.Close(); return nil },
		})
		return pool, nil
	}),
	fx.Provide(func(pool *pgxpool.Pool) app.UnitOfWork { return postgres.NewUoW(pool) }),
)

var sqsModule = fx.Module("sqs",
	fx.Provide(func(lc fx.Lifecycle, cfg config.Config) (*sqsadapter.Client, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return sqsadapter.NewClient(ctx, cfg)
	}),
	fx.Provide(func(c *sqsadapter.Client) app.EventPublisher { return c }),
)

var authModule = fx.Module("auth",
	fx.Provide(func(cfg config.Config) (*auth.Verifier, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return auth.NewVerifier(ctx, cfg)
	}),
)

var appModule = fx.Module("app",
	fx.Provide(func() app.Clock { return app.SystemClock{} }),
	fx.Provide(func() app.IDGen { return app.UUIDGen{} }),
	fx.Provide(func(uow app.UnitOfWork, clock app.Clock, ids app.IDGen, cfg config.Config) *app.Service {
		return app.NewService(uow, clock, ids, app.Config{
			MaxReferenceAttempts: cfg.MaxReferenceAttempts,
			ReferenceBackoff:     cfg.ReferenceBackoff,
		})
	}),
)

var httpModule = fx.Module("http",
	fx.Provide(httpapi.NewMetrics),
	fx.Provide(func(pool *pgxpool.Pool, sqs *sqsadapter.Client) *health.Checker {
		return health.New(pool, sqs)
	}),
	fx.Provide(func(svc *app.Service, v *auth.Verifier, h *health.Checker, m *httpapi.Metrics) *httpapi.Server {
		return httpapi.NewServer(svc, v, h, m)
	}),
	fx.Invoke(func(lc fx.Lifecycle, cfg config.Config, srv *httpapi.Server, log *zap.Logger) {
		server := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Router()}
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				log.Info("http listening", zap.String("addr", cfg.HTTPAddr))
				go func() { _ = server.ListenAndServe() }()
				return nil
			},
			OnStop: func(ctx context.Context) error {
				shutdownCtx, cancel := context.WithTimeout(ctx, cfg.ShutdownTimeout)
				defer cancel()
				return server.Shutdown(shutdownCtx)
			},
		})
	}),
)

var workersModule = fx.Module("workers",
	fx.Provide(func(c *sqsadapter.Client, svc *app.Service, log *zap.Logger) *sqsadapter.Consumer {
		return sqsadapter.NewConsumer(c, svc, log)
	}),
	fx.Provide(func(uow app.UnitOfWork, c *sqsadapter.Client, cfg config.Config, log *zap.Logger) *sqsadapter.Publisher {
		return sqsadapter.NewPublisher(uow, c, cfg.InstanceID, log)
	}),
	fx.Provide(func(svc *app.Service, log *zap.Logger) *sqsadapter.ReferenceWorker {
		return sqsadapter.NewReferenceWorker(svc, log)
	}),
	fx.Invoke(func(lc fx.Lifecycle, consumer *sqsadapter.Consumer, pub *sqsadapter.Publisher, refs *sqsadapter.ReferenceWorker, log *zap.Logger) {
		ctx, cancel := context.WithCancel(context.Background())
		lc.Append(fx.Hook{
			OnStart: func(_ context.Context) error {
				go func() { _ = consumer.Run(ctx) }()
				go func() { _ = pub.Run(ctx) }()
				go func() { _ = refs.Run(ctx) }()
				return nil
			},
			OnStop: func(_ context.Context) error {
				cancel()
				log.Info("workers stopping")
				return nil
			},
		})
	}),
)
