package config

import (
	"os"
	"strconv"
)

type Config struct {
	ListenAddr                     string
	JobStorePath                   string
	RunRootPath                    string
	RepoRootPath                   string
	AuditLogPath                   string
	AuditVerifyMode                string
	AuditRotateBytes               int64
	AuditKeepArchives              int
	AuditMaintainIntervalSeconds   int
	AuthMode                       string
	StaticTokens                   string
	MCPActor                       string
	MCPRole                        string
	BackendKind                    string
	SlurmMode                      string
	SlurmSubmitCmd                 string
	SlurmStatusCmd                 string
	SlurmCancelCmd                 string
	SlurmScriptPath                string
	SlurmPartitionCPU              string
	SlurmPartitionP40              string
	SlurmPartitionA100             string
	SlurmNodeListCPU               string
	SlurmNodeListP40               string
	SlurmNodeListA100              string
	SlurmConstraintCPU             string
	SlurmConstraintP40             string
	SlurmConstraintA100            string
	ModelProfileCPU                string
	ModelProfileP40                string
	ModelProfileA100               string
	RuntimeLlamaCPPBaseURL         string
	RuntimeLlamaCPPTimeoutSeconds  int
	RuntimeVLLMBaseURL             string
	RuntimeVLLMTimeoutSeconds      int
	RuntimeSGLangBaseURL           string
	RuntimeSGLangTimeoutSeconds    int
	LocalMode                      string
	LocalScriptPath                string
	ParallelMaxBatchSize           int
	ParallelMaxActiveBatches       int
	RootActionMaxAdditionalBatches int
	RootActionMaxRetriedShards     int
}

func Load() Config {
	return Config{
		ListenAddr:                     envOrDefault("BROKER_LISTEN_ADDR", ":8081"),
		JobStorePath:                   envOrDefault("BROKER_JOB_STORE_PATH", ".broker/jobs.json"),
		RunRootPath:                    envOrDefault("BROKER_RUN_ROOT_PATH", ".broker/runs"),
		RepoRootPath:                   envOrDefault("BROKER_REPO_ROOT_PATH", "."),
		AuditLogPath:                   envOrDefault("BROKER_AUDIT_LOG_PATH", ".broker/audit.jsonl"),
		AuditVerifyMode:                envOrDefault("BROKER_AUDIT_VERIFY_MODE", "fail"),
		AuditRotateBytes:               envOrDefaultInt64("BROKER_AUDIT_ROTATE_BYTES", 10*1024*1024),
		AuditKeepArchives:              envOrDefaultInt("BROKER_AUDIT_KEEP_ARCHIVES", 10),
		AuditMaintainIntervalSeconds:   envOrDefaultInt("BROKER_AUDIT_MAINTAIN_INTERVAL_SECONDS", 300),
		AuthMode:                       envOrDefault("BROKER_AUTH_MODE", "header"),
		StaticTokens:                   envOrDefault("BROKER_STATIC_TOKENS", ""),
		MCPActor:                       envOrDefault("BROKER_MCP_ACTOR", ""),
		MCPRole:                        envOrDefault("BROKER_MCP_ROLE", "user"),
		BackendKind:                    envOrDefault("BROKER_BACKEND", "slurm"),
		SlurmMode:                      envOrDefault("BROKER_SLURM_MODE", "stub"),
		SlurmSubmitCmd:                 envOrDefault("BROKER_SLURM_SUBMIT_CMD", "sbatch"),
		SlurmStatusCmd:                 envOrDefault("BROKER_SLURM_STATUS_CMD", "sacct"),
		SlurmCancelCmd:                 envOrDefault("BROKER_SLURM_CANCEL_CMD", "scancel"),
		SlurmScriptPath:                envOrDefault("BROKER_SLURM_SCRIPT_PATH", "deploy/slurm/broker_worker.slurm"),
		SlurmPartitionCPU:              envOrDefault("BROKER_SLURM_PARTITION_CPU", ""),
		SlurmPartitionP40:              envOrDefault("BROKER_SLURM_PARTITION_P40", ""),
		SlurmPartitionA100:             envOrDefault("BROKER_SLURM_PARTITION_A100", ""),
		SlurmNodeListCPU:               envOrDefault("BROKER_SLURM_NODELIST_CPU", ""),
		SlurmNodeListP40:               envOrDefault("BROKER_SLURM_NODELIST_P40", ""),
		SlurmNodeListA100:              envOrDefault("BROKER_SLURM_NODELIST_A100", ""),
		SlurmConstraintCPU:             envOrDefault("BROKER_SLURM_CONSTRAINT_CPU", ""),
		SlurmConstraintP40:             envOrDefault("BROKER_SLURM_CONSTRAINT_P40", ""),
		SlurmConstraintA100:            envOrDefault("BROKER_SLURM_CONSTRAINT_A100", ""),
		ModelProfileCPU:                envOrDefault("BROKER_MODEL_PROFILE_CPU", ""),
		ModelProfileP40:                envOrDefault("BROKER_MODEL_PROFILE_P40", "gpt-oss-20b.p40"),
		ModelProfileA100:               envOrDefault("BROKER_MODEL_PROFILE_A100", "qwen3-coder-30b.a100"),
		RuntimeLlamaCPPBaseURL:         envOrDefault("BROKER_RUNTIME_LLAMACPP_BASE_URL", ""),
		RuntimeLlamaCPPTimeoutSeconds:  envOrDefaultInt("BROKER_RUNTIME_LLAMACPP_TIMEOUT_SECONDS", 20),
		RuntimeVLLMBaseURL:             envOrDefault("BROKER_RUNTIME_VLLM_BASE_URL", ""),
		RuntimeVLLMTimeoutSeconds:      envOrDefaultInt("BROKER_RUNTIME_VLLM_TIMEOUT_SECONDS", 20),
		RuntimeSGLangBaseURL:           envOrDefault("BROKER_RUNTIME_SGLANG_BASE_URL", ""),
		RuntimeSGLangTimeoutSeconds:    envOrDefaultInt("BROKER_RUNTIME_SGLANG_TIMEOUT_SECONDS", 20),
		LocalMode:                      envOrDefault("BROKER_LOCAL_MODE", "command"),
		LocalScriptPath:                envOrDefault("BROKER_LOCAL_SCRIPT_PATH", "deploy/local/broker_worker.sh"),
		ParallelMaxBatchSize:           envOrDefaultInt("BROKER_PARALLEL_MAX_BATCH_SIZE", 64),
		ParallelMaxActiveBatches:       envOrDefaultInt("BROKER_PARALLEL_MAX_ACTIVE_BATCHES", 0),
		RootActionMaxAdditionalBatches: envOrDefaultInt("BROKER_ROOT_ACTION_MAX_ADDITIONAL_BATCHES", 1),
		RootActionMaxRetriedShards:     envOrDefaultInt("BROKER_ROOT_ACTION_MAX_RETRIED_SHARDS", 4),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func envOrDefaultInt64(key string, fallback int64) int64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return parsed
		}
	}
	return fallback
}
