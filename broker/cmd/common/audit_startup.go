package common

import (
	"fmt"
	"log"
	"strings"

	"github.com/limr/ollama-slurm/broker/pkg/audit"
)

func VerifyAuditStartup(logger *log.Logger, path, mode string) error {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "fail"
	}
	if mode == "off" {
		logger.Printf("audit verification skipped path=%s", path)
		return nil
	}

	result, err := audit.VerifyFile(path)
	if err != nil {
		return fmt.Errorf("verify audit log: %w", err)
	}
	if result.Valid {
		logger.Printf("audit verification ok path=%s events=%d last_hash=%s", result.Path, result.EventCount, result.LastHash)
		return nil
	}

	switch mode {
	case "warn":
		logger.Printf("audit verification warning path=%s line=%d message=%s", result.Path, result.FailureLine, result.Message)
		return nil
	case "fail":
		return fmt.Errorf("audit verification failed path=%s line=%d message=%s", result.Path, result.FailureLine, result.Message)
	default:
		return fmt.Errorf("unsupported BROKER_AUDIT_VERIFY_MODE: %s", mode)
	}
}
