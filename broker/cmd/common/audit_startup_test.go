package common

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/limr/ollama-slurm/broker/pkg/audit"
)

func TestVerifyAuditStartupFailModeRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger := audit.NewFileLogger(path)
	if err := logger.Log(context.Background(), audit.Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
		t.Fatalf("log first event: %v", err)
	}
	if err := logger.Log(context.Background(), audit.Event{Actor: "bob", Action: "job.cancel", Outcome: "forbidden"}); err != nil {
		t.Fatalf("log second event: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	tampered := strings.Replace(string(data), `"outcome":"forbidden"`, `"outcome":"success"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered file: %v", err)
	}

	var logBuf bytes.Buffer
	if err := VerifyAuditStartup(log.New(&logBuf, "", 0), path, "fail"); err == nil {
		t.Fatal("expected startup verification failure")
	}
}

func TestVerifyAuditStartupWarnModeLogsButContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger := audit.NewFileLogger(path)
	if err := logger.Log(context.Background(), audit.Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
		t.Fatalf("log event: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	tampered := strings.Replace(string(data), `"outcome":"success"`, `"outcome":"forbidden"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered file: %v", err)
	}

	var logBuf bytes.Buffer
	if err := VerifyAuditStartup(log.New(&logBuf, "", 0), path, "warn"); err != nil {
		t.Fatalf("expected warn mode to continue: %v", err)
	}
	if !strings.Contains(logBuf.String(), "audit verification warning") {
		t.Fatalf("expected warning log, got %q", logBuf.String())
	}
}

func TestVerifyAuditStartupOffModeSkipsFailure(t *testing.T) {
	var logBuf bytes.Buffer
	if err := VerifyAuditStartup(log.New(&logBuf, "", 0), filepath.Join(t.TempDir(), "missing.jsonl"), "off"); err != nil {
		t.Fatalf("expected off mode to skip verification: %v", err)
	}
}
