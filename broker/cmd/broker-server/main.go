package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/limr/ollama-slurm/broker/cmd/common"
	"github.com/limr/ollama-slurm/broker/pkg/api"
	"github.com/limr/ollama-slurm/broker/pkg/audit"
	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/backends"
	"github.com/limr/ollama-slurm/broker/pkg/backends/local"
	"github.com/limr/ollama-slurm/broker/pkg/backends/slurm"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/service"
	"github.com/limr/ollama-slurm/broker/pkg/store"
)

func main() {
	cfg := config.Load()
	logger := log.New(os.Stdout, "broker-server ", log.LstdFlags|log.LUTC)
	if err := common.VerifyAuditStartup(logger, cfg.AuditLogPath, cfg.AuditVerifyMode); err != nil {
		logger.Fatalf("audit startup verification failed: %v", err)
	}

	jobStore, err := store.NewFileJobStore(cfg.JobStorePath)
	if err != nil {
		logger.Fatalf("initialize job store: %v", err)
	}
	backend, err := buildBackend(cfg)
	if err != nil {
		logger.Fatalf("initialize backend: %v", err)
	}
	auditLogger := audit.NewFileLogger(cfg.AuditLogPath)
	svc := service.NewWithAuditAndOptionsAndConfig(jobStore, backend, logger, auditLogger, cfg.RunRootPath, cfg.RepoRootPath, service.Options{
		ParallelMaxBatchSize:           cfg.ParallelMaxBatchSize,
		ParallelMaxActiveBatches:       cfg.ParallelMaxActiveBatches,
		RootActionMaxAdditionalBatches: cfg.RootActionMaxAdditionalBatches,
		RootActionMaxRetriedShards:     cfg.RootActionMaxRetriedShards,
	}, &cfg)
	authenticator, err := buildAuthenticator(cfg)
	if err != nil {
		logger.Fatalf("initialize authenticator: %v", err)
	}
	handler := api.NewHandlerWithAudit(svc, authenticator, cfg.AuditLogPath)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Printf("listening on %s", cfg.ListenAddr)
		errCh <- server.ListenAndServe()
	}()

	stopMaintenance := startAuditMaintenance(logger, cfg)
	defer stopMaintenance()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Printf("received signal %s, shutting down", sig)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Fatalf("server failed: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Fatalf("shutdown failed: %v", err)
	}
}

func buildAuthenticator(cfg config.Config) (*auth.Authenticator, error) {
	switch cfg.AuthMode {
	case "", "header":
		return auth.NewHeaderAuthenticator(), nil
	case "static_tokens":
		tokens, err := auth.ParseStaticTokens(cfg.StaticTokens)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 0 {
			return nil, errUnsupportedAuthMode("static_tokens requires BROKER_STATIC_TOKENS")
		}
		return auth.NewStaticTokenAuthenticator(tokens), nil
	default:
		return nil, errUnsupportedAuthMode(cfg.AuthMode)
	}
}

type unsupportedAuthModeError string

func (e unsupportedAuthModeError) Error() string {
	return "unsupported auth mode: " + string(e)
}

func errUnsupportedAuthMode(mode string) error {
	return unsupportedAuthModeError(mode)
}

func buildBackend(cfg config.Config) (backends.Backend, error) {
	switch cfg.BackendKind {
	case "", "slurm":
		return slurm.NewBackend(cfg), nil
	case "local":
		return local.NewBackend(cfg), nil
	default:
		return nil, errUnsupportedBackend(cfg.BackendKind)
	}
}

type unsupportedBackendError string

func (e unsupportedBackendError) Error() string {
	return "unsupported backend: " + string(e)
}

func errUnsupportedBackend(name string) error {
	return unsupportedBackendError(name)
}

func startAuditMaintenance(logger *log.Logger, cfg config.Config) func() {
	if cfg.AuditRotateBytes <= 0 || cfg.AuditMaintainIntervalSeconds <= 0 {
		return func() {}
	}
	ticker := time.NewTicker(time.Duration(cfg.AuditMaintainIntervalSeconds) * time.Second)
	stopCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				result, err := audit.MaintainFile(cfg.AuditLogPath, cfg.AuditRotateBytes, cfg.AuditKeepArchives, time.Now().UTC())
				if err != nil {
					logger.Printf("audit maintenance failed: %v", err)
					continue
				}
				if result.Rotated || len(result.Removed) > 0 {
					logger.Printf("audit maintenance rotated=%t archive=%s removed=%d retained=%d", result.Rotated, result.ArchivePath, len(result.Removed), result.Retained)
				}
			case <-stopCh:
				ticker.Stop()
				return
			}
		}
	}()
	return func() {
		close(stopCh)
	}
}
