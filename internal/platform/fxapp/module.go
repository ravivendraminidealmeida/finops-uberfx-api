package fxapp

import (
	"context"
	"errors"
	"net/http"
	"sync"
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
	"junglegaming.io/finops-api/internal/platform/obs"
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
	fx.Provide(func(cfg config.Config) (*sqsadapter.Client, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return sqsadapter.NewClient(ctx, cfg)
	}),
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
	fx.Provide(obs.NewMetrics),
	fx.Provide(func(pool *pgxpool.Pool, sqs *sqsadapter.Client) httpapi.ReadyFunc {
		return func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return err
			}
			return sqs.Ready(ctx)
		}
	}),
	fx.Provide(httpapi.NewServer),
	fx.Invoke(func(lc fx.Lifecycle, cfg config.Config, srv *httpapi.Server, log *zap.Logger, shutdowner fx.Shutdowner) {
		server := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Router()}
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				log.Info("http listening", zap.String("addr", cfg.HTTPAddr))
				go func() {
					if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
						log.Error("http server failed", zap.Error(err))
						_ = shutdowner.Shutdown()
					}
				}()
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
	fx.Provide(sqsadapter.NewConsumer),
	fx.Provide(func(uow app.UnitOfWork, c *sqsadapter.Client, cfg config.Config, clock app.Clock, log *zap.Logger, m *obs.Metrics) *sqsadapter.Publisher {
		return sqsadapter.NewPublisher(uow, c, cfg.InstanceID, clock, log, m)
	}),
	fx.Provide(sqsadapter.NewReferenceWorker),
	fx.Invoke(func(lc fx.Lifecycle, consumer *sqsadapter.Consumer, pub *sqsadapter.Publisher, refs *sqsadapter.ReferenceWorker, log *zap.Logger) {
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		lc.Append(fx.Hook{
			OnStart: func(_ context.Context) error {
				start := func(run func(context.Context) error) {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = run(ctx)
					}()
				}
				start(consumer.Run)
				start(pub.Run)
				start(refs.Run)
				return nil
			},
			OnStop: func(stopCtx context.Context) error {
				cancel()
				log.Info("workers stopping")
				done := make(chan struct{})
				go func() {
					wg.Wait()
					close(done)
				}()
				select {
				case <-done:
					return nil
				case <-stopCtx.Done():
					return stopCtx.Err()
				}
			},
		})
	}),
)
