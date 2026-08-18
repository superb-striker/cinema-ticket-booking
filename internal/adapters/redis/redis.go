package redis

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type Config struct {
	Addr          string
	Username      string
	Password      string
	DB            int
	TLSEnabled    bool
	TLSServerName string
	TLSCAFile     string
	TLSCertFile   string
	TLSKeyFile    string
	PoolSize      int
	MinIdleConns  int
}

// NewClient connects to Redis and returns an error instead of exiting, so callers decide how to handle failure.
func NewClient(addr string) (*goredis.Client, error) {
	return Open(Config{Addr: addr})
}

func NewClientWithPassword(addr, password string) (*goredis.Client, error) {
	return Open(Config{Addr: addr, Password: password})
}

func Open(config Config) (*goredis.Client, error) {
	tlsConfig, err := redisTLSConfig(config)
	if err != nil {
		return nil, err
	}
	rdb := goredis.NewClient(&goredis.Options{
		Addr:         config.Addr,
		Username:     config.Username,
		Password:     config.Password,
		DB:           config.DB,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		PoolSize:     config.PoolSize,
		MinIdleConns: config.MinIdleConns,
		TLSConfig:    tlsConfig,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return rdb, nil
}

func redisTLSConfig(config Config) (*tls.Config, error) {
	if !config.TLSEnabled {
		return nil, nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: config.TLSServerName}
	if config.TLSCAFile != "" {
		pem, err := os.ReadFile(config.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read Redis CA: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("Redis CA file contains no valid certificates")
		}
		tlsConfig.RootCAs = roots
	}
	if config.TLSCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load Redis client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	return tlsConfig, nil
}
