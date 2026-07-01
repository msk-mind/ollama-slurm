package e2e

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/backends/slurm"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/service"
	"github.com/limr/ollama-slurm/broker/pkg/store"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func TestCommandModeDocumentSummarySmoke(t *testing.T) {
	if _, err := os.Stat("/usr/bin/bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 not available")
	}

	baseDir := t.TempDir()
	runRoot := filepath.Join(baseDir, "runs")

	repoDir := repoRoot(t)
	setupFakeSlurmEnv(t, repoDir, baseDir)

	inputPath := filepath.Join(baseDir, "source.txt")
	writeTestFile(t, inputPath, "Smoke test document.\n- alpha\n- beta\n")

	cfg := config.Config{
		SlurmMode:       "command",
		SlurmSubmitCmd:  "sbatch",
		SlurmStatusCmd:  "sacct",
		SlurmCancelCmd:  "scancel",
		SlurmScriptPath: filepath.Join(repoDir, "deploy", "slurm", "broker_worker.slurm"),
	}

	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(cfg),
		log.New(io.Discard, "", 0),
		runRoot,
		repoDir,
	)

	submitResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "document_summary",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + inputPath, ContentHash: "sha256:test"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	job := waitForJob(t, svc, submitResp.JobID, 5*time.Second)

	if job.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded, got %q", job.State)
	}
	if job.Result == nil {
		t.Fatal("expected ingested result")
	}
	if !strings.Contains(job.Result.SchemaName, "document_summary_v1") {
		t.Fatalf("unexpected schema name %q", job.Result.SchemaName)
	}
}
