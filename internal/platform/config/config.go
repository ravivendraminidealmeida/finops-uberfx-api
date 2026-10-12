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
	dsn, err := DatabaseURL()
	if err != nil {
		return Config{}, err
	}
	awsKey := os.Getenv("AWS_ACCESS_KEY_ID")
	awsSecret := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if awsKey == "" || awsSecret == "" {
		return Config{}, fmt.Errorf("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are required")
	}
	issuer := os.Getenv("OIDC_ISSUER")
	if issuer == "" {
		return Config{}, fmt.Errorf("OIDC_ISSUER is required")
	}
	instance := os.Getenv("INSTANCE_ID")
	if instance == "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "local"
		}
		instance = host
	}
	return Config{
		HTTPAddr:             env("HTTP_ADDR", ":8080"),
		DatabaseURL:          dsn,
		AWSRegion:            env("AWS_REGION", "us-east-1"),
		AWSEndpoint:          os.Getenv("AWS_ENDPOINT_URL"),
		AWSAccessKey:         awsKey,
		AWSSecretKey:         awsSecret,
		WagerQueueName:       env("WAGER_QUEUE_NAME", "wager-transactions.fifo"),
		DLQName:              env("WAGER_DLQ_NAME", "wager-transactions-dlq.fifo"),
		EventsQueueName:      env("EVENTS_QUEUE_NAME", "domain-events.fifo"),
		OIDCIssuer:           issuer,
		OIDCAudience:         env("OIDC_AUDIENCE", "finops-api"),
		InternalClientID:     env("INTERNAL_CLIENT_ID", "wallet-internal"),
		MaxReferenceAttempts: envInt("PENDING_REF_MAX_ATTEMPTS", 8),
		ReferenceBackoff:     time.Duration(envInt("PENDING_REF_BACKOFF_MS", 200)) * time.Millisecond,
		ShutdownTimeout:      time.Duration(envInt("SHUTDOWN_TIMEOUT_SEC", 15)) * time.Second,
		InstanceID:           instance,
	}, nil
}

func DatabaseURL() (string, error) {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v, nil
	}
	user := env("DATABASE_USER", "finops")
	pass := os.Getenv("DATABASE_PASSWORD")
	if pass == "" {
		return "", fmt.Errorf("DATABASE_URL or DATABASE_PASSWORD is required")
	}
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
