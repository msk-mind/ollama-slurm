package audit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryLoggerChainsHashes(t *testing.T) {
	logger := NewMemoryLogger()

	if err := logger.Log(context.Background(), Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
		t.Fatalf("log first event: %v", err)
	}
	if err := logger.Log(context.Background(), Event{Actor: "alice", Action: "job.get_status", Outcome: "success"}); err != nil {
		t.Fatalf("log second event: %v", err)
	}

	if len(logger.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(logger.Events))
	}
	first := logger.Events[0]
	second := logger.Events[1]
	if first.EventHash == "" {
		t.Fatal("expected first event hash")
	}
	if second.PrevHash != first.EventHash {
		t.Fatalf("expected chained prev_hash, got %#v", second)
	}
}

func TestFileLoggerChainsHashesAcrossWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger := NewFileLogger(path)

	if err := logger.Log(context.Background(), Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
		t.Fatalf("log first event: %v", err)
	}
	if err := logger.Log(context.Background(), Event{Actor: "bob", Action: "job.cancel", Outcome: "forbidden"}); err != nil {
		t.Fatalf("log second event: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var first, second Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first event: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decode second event: %v", err)
	}
	if first.EventHash == "" {
		t.Fatal("expected first event hash")
	}
	if second.PrevHash != first.EventHash {
		t.Fatalf("expected second prev_hash=%q, got %q", first.EventHash, second.PrevHash)
	}
	if second.EventHash == "" {
		t.Fatal("expected second event hash")
	}
}

func TestVerifyFileSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger := NewFileLogger(path)

	if err := logger.Log(context.Background(), Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
		t.Fatalf("log first event: %v", err)
	}
	if err := logger.Log(context.Background(), Event{Actor: "bob", Action: "job.cancel", Outcome: "forbidden"}); err != nil {
		t.Fatalf("log second event: %v", err)
	}

	result, err := VerifyFile(path)
	if err != nil {
		t.Fatalf("verify file: %v", err)
	}
	if !result.Valid || result.EventCount != 2 {
		t.Fatalf("unexpected verification result: %#v", result)
	}
}

func TestVerifyFileDetectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger := NewFileLogger(path)

	if err := logger.Log(context.Background(), Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
		t.Fatalf("log first event: %v", err)
	}
	if err := logger.Log(context.Background(), Event{Actor: "bob", Action: "job.cancel", Outcome: "forbidden"}); err != nil {
		t.Fatalf("log second event: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	tampered := strings.Replace(string(data), `"outcome":"forbidden"`, `"outcome":"success"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered audit file: %v", err)
	}

	result, err := VerifyFile(path)
	if err != nil {
		t.Fatalf("verify tampered file: %v", err)
	}
	if result.Valid || result.FailureLine != 2 {
		t.Fatalf("expected invalid verification result, got %#v", result)
	}
}

func TestRotateFilePreservesChainAcrossSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger := NewFileLogger(path)

	if err := logger.Log(context.Background(), Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
		t.Fatalf("log first event: %v", err)
	}
	if err := logger.Log(context.Background(), Event{Actor: "bob", Action: "job.cancel", Outcome: "forbidden"}); err != nil {
		t.Fatalf("log second event: %v", err)
	}

	rotated, err := RotateFile(path, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("rotate file: %v", err)
	}
	if !rotated.Rotated || rotated.ArchivePath == "" || rotated.LastHash == "" {
		t.Fatalf("unexpected rotation result: %#v", rotated)
	}

	if err := logger.Log(context.Background(), Event{Actor: "carol", Action: "job.get_status", Outcome: "success"}); err != nil {
		t.Fatalf("log post-rotation event: %v", err)
	}

	archiveResult, err := VerifyFile(rotated.ArchivePath)
	if err != nil {
		t.Fatalf("verify archive: %v", err)
	}
	if !archiveResult.Valid || archiveResult.EventCount != 2 {
		t.Fatalf("unexpected archive verification: %#v", archiveResult)
	}

	activeResult, err := VerifyFile(path)
	if err != nil {
		t.Fatalf("verify active: %v", err)
	}
	if !activeResult.Valid || activeResult.EventCount != 1 {
		t.Fatalf("unexpected active verification: %#v", activeResult)
	}
}

func TestPruneArchivesKeepsNewestSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger := NewFileLogger(path)

	for i := 0; i < 3; i++ {
		if err := logger.Log(context.Background(), Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
			t.Fatalf("log event %d: %v", i, err)
		}
		_, err := RotateFile(path, time.Date(2026, 6, 26, 12, 0, i, 0, time.UTC))
		if err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		}
	}

	result, err := PruneArchives(path, 1)
	if err != nil {
		t.Fatalf("prune archives: %v", err)
	}
	if len(result.Removed) != 2 || result.Retained != 1 {
		t.Fatalf("unexpected prune result: %#v", result)
	}

	archives, err := listArchiveFiles(path)
	if err != nil {
		t.Fatalf("list archives: %v", err)
	}
	if len(archives) != 1 {
		t.Fatalf("expected 1 retained archive, got %d", len(archives))
	}
}

func TestMaintainFileRotatesAndPrunes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger := NewFileLogger(path)

	for i := 0; i < 3; i++ {
		if err := logger.Log(context.Background(), Event{
			Actor:   "alice",
			Action:  "job.submit",
			Outcome: "success",
			Fields:  map[string]any{"index": i, "payload": strings.Repeat("x", 64)},
		}); err != nil {
			t.Fatalf("log event %d: %v", i, err)
		}
		if i < 2 {
			if _, err := RotateFile(path, time.Date(2026, 6, 26, 12, 0, i, 0, time.UTC)); err != nil {
				t.Fatalf("pre-rotate %d: %v", i, err)
			}
		}
	}

	result, err := MaintainFile(path, 1, 1, time.Date(2026, 6, 26, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("maintain file: %v", err)
	}
	if !result.Rotated {
		t.Fatalf("expected rotation, got %#v", result)
	}
	if len(result.Removed) != 2 || result.Retained != 1 {
		t.Fatalf("unexpected prune state: %#v", result)
	}
}
