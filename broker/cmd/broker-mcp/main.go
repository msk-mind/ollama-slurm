package main

import (
	"context"
	"log"
	"os"

	"github.com/limr/ollama-slurm/broker/cmd/common"
	"github.com/limr/ollama-slurm/broker/pkg/audit"
	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/backends"
	"github.com/limr/ollama-slurm/broker/pkg/backends/local"
	"github.com/limr/ollama-slurm/broker/pkg/backends/slurm"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/mcp"
	"github.com/limr/ollama-slurm/broker/pkg/service"
	"github.com/limr/ollama-slurm/broker/pkg/store"
)

func main() {
	cfg := config.Load()
	logger := log.New(os.Stderr, "broker-mcp ", log.LstdFlags|log.LUTC)
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

	svc := service.NewWithAuditAndOptionsAndConfig(
		jobStore,
		backend,
		logger,
		audit.NewFileLogger(cfg.AuditLogPath),
		cfg.RunRootPath,
		cfg.RepoRootPath,
		service.Options{
			ParallelMaxBatchSize:           cfg.ParallelMaxBatchSize,
			ParallelMaxActiveBatches:       cfg.ParallelMaxActiveBatches,
			RootActionMaxAdditionalBatches: cfg.RootActionMaxAdditionalBatches,
			RootActionMaxRetriedShards:     cfg.RootActionMaxRetriedShards,
		},
		&cfg,
	)

	server := mcp.NewServer(svc, auth.Principal{
		Actor: cfg.MCPActor,
		Role:  cfg.MCPRole,
	})
	if err := server.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		logger.Fatalf("serve mcp: %v", err)
	}
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
