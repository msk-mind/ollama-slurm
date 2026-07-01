package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupFakeSlurmEnv(t *testing.T, repoRoot, baseDir string) {
	t.Helper()

	scriptPath := filepath.Join(repoRoot, "tests", "e2e", "fake_slurm_env.sh")
	if _, err := exec.Command("/usr/bin/bash", scriptPath, baseDir).Output(); err != nil {
		t.Fatalf("setup fake slurm env: %v", err)
	}

	binDir := filepath.Join(baseDir, "bin")
	stateDir := filepath.Join(baseDir, "fake-slurm")
	if _, err := os.Stat(filepath.Join(binDir, "sbatch")); err != nil {
		t.Fatalf("expected fake sbatch: %v", err)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("expected fake state dir: %v", err)
	}

	t.Setenv("FAKE_SLURM_STATE_DIR", stateDir)
	t.Setenv("FAKE_SLURM_BASH", "/usr/bin/bash")
	t.Setenv("FAKE_SLURM_PYTHON", "/usr/bin/python3")
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
}
