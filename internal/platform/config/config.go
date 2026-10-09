package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr             string
	DatabaseURL          string
	AWSRegion            string
	AWSEndpoint          string
	AWSAccessKey         string
	AWSSecretKey         string
	WagerQueueName       string
	DLQName              string
	EventsQueueName      string
	OIDCIssuer           string
	OIDCAudience         string
	InternalClientID     string
	MaxReferenceAttempts int
	ReferenceBackoff     time.Duration
	ShutdownTimeout      time.Duration
	InstanceID           string
}

func Load() (Config, error) {
	dsn, err := databaseURL()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		HTTPAddr:             env("HTTP_ADDR", ":8080"),
		DatabaseURL:          dsn,
		AWSRegion:            env("AWS_REGION", "us-east-1"),
		AWSEndpoint:          env("AWS_ENDPOINT_URL", "http://localhost:4566"),
		AWSAccessKey:         env("AWS_ACCESS_KEY_ID", "NU_TEST_localstack"),
		AWSSecretKey:         env("AWS_SECRET_ACCESS_KEY", "NU_TEST_localstack"),
		WagerQueueName:       env("WAGER_QUEUE_NAME", "wager-transactions.fifo"),
		DLQName:              env("WAGER_DLQ_NAME", "wager-transactions-dlq.fifo"),
		EventsQueueName:      env("EVENTS_QUEUE_NAME", "domain-events.fifo"),
		OIDCIssuer:           env("OIDC_ISSUER", "http://localhost:8081/realms/finops"),
		OIDCAudience:         env("OIDC_AUDIENCE", "finops-api"),
		InternalClientID:     env("INTERNAL_CLIENT_ID", "wallet-internal"),
		MaxReferenceAttempts: envInt("PENDING_REF_MAX_ATTEMPTS", 8),
		ReferenceBackoff:     time.Duration(envInt("PENDING_REF_BACKOFF_MS", 1000)) * time.Millisecond,
		ShutdownTimeout:      time.Duration(envInt("SHUTDOWN_TIMEOUT_SEC", 15)) * time.Second,
		InstanceID:           env("INSTANCE_ID", "local"),
	}
	if cfg.OIDCIssuer == "" {
		return Config{}, fmt.Errorf("OIDC_ISSUER is required")
	}
	return cfg, nil
}

func databaseURL() (string, error) {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v, nil
	}
	user := env("DATABASE_USER", "finops")
	pass := env("DATABASE_PASSWORD", "NU_TEST_finops") // NOT VALID — test data only
	host := env("DATABASE_HOST", "localhost")
	port := env("DATABASE_PORT", "5432")
	name := env("DATABASE_NAME", "finops")
	ssl := env("DATABASE_SSLMODE", "disable")
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, pass),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + name,
		RawQuery: "sslmode=" + ssl,
	}
	return u.String(), nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return fallback
}
