package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Event struct {
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor,omitempty"`
	Role      string         `json:"role,omitempty"`
	Action    string         `json:"action"`
	Outcome   string         `json:"outcome"`
	JobID     string         `json:"job_id,omitempty"`
	TaskType  string         `json:"task_type,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
	PrevHash  string         `json:"prev_hash,omitempty"`
	EventHash string         `json:"event_hash,omitempty"`
}

type Logger interface {
	Log(context.Context, Event) error
}

type VerificationResult struct {
	Path        string `json:"path"`
	Valid       bool   `json:"valid"`
	EventCount  int    `json:"event_count"`
	LastHash    string `json:"last_hash,omitempty"`
	FailureLine int    `json:"failure_line,omitempty"`
	Message     string `json:"message,omitempty"`
}

type RotationResult struct {
	Path        string `json:"path"`
	ArchivePath string `json:"archive_path,omitempty"`
	LastHash    string `json:"last_hash,omitempty"`
	Rotated     bool   `json:"rotated"`
	Message     string `json:"message,omitempty"`
}

type PruneResult struct {
	Path         string   `json:"path"`
	KeepArchives int      `json:"keep_archives"`
	Removed      []string `json:"removed,omitempty"`
	Retained     int      `json:"retained"`
	Message      string   `json:"message,omitempty"`
}

type MaintenanceResult struct {
	Path         string         `json:"path"`
	Rotated      bool           `json:"rotated"`
	ArchivePath  string         `json:"archive_path,omitempty"`
	LastHash     string         `json:"last_hash,omitempty"`
	Removed      []string       `json:"removed,omitempty"`
	Retained     int            `json:"retained"`
	KeepArchives int            `json:"keep_archives"`
	Message      string         `json:"message,omitempty"`
	Rotation     RotationResult `json:"rotation"`
	Prune        PruneResult    `json:"prune"`
}

type NopLogger struct{}

func NewNopLogger() Logger {
	return NopLogger{}
}

func (NopLogger) Log(context.Context, Event) error {
	return nil
}

type FileLogger struct {
	mu   sync.Mutex
	path string
}

func NewFileLogger(path string) *FileLogger {
	return &FileLogger{path: path}
}

func (l *FileLogger) Log(_ context.Context, event Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	prevHash, err := readPreviousHash(l.path)
	if err != nil {
		return err
	}
	event.PrevHash = prevHash
	event.EventHash, err = computeEventHash(event)
	if err != nil {
		return err
	}

	handle, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer handle.Close()

	encoder := json.NewEncoder(handle)
	return encoder.Encode(event)
}

type MemoryLogger struct {
	mu     sync.Mutex
	Events []Event
}

func NewMemoryLogger() *MemoryLogger {
	return &MemoryLogger{}
}

func (l *MemoryLogger) Log(_ context.Context, event Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if len(l.Events) > 0 {
		event.PrevHash = l.Events[len(l.Events)-1].EventHash
	}
	hash, err := computeEventHash(event)
	if err != nil {
		return err
	}
	event.EventHash = hash
	l.Events = append(l.Events, event)
	return nil
}

func computeEventHash(event Event) (string, error) {
	event.EventHash = ""
	payload, err := marshalCanonical(event)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func marshalCanonical(event Event) ([]byte, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	var normalized bytes.Buffer
	if err := json.Compact(&normalized, payload); err != nil {
		return nil, err
	}
	return normalized.Bytes(), nil
}

func readLastEventHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		return "", nil
	}
	var event Event
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &event); err != nil {
		return "", fmt.Errorf("decode previous audit event: %w", err)
	}
	return event.EventHash, nil
}

func readPreviousHash(path string) (string, error) {
	lastHash, err := readLastEventHash(path)
	if err != nil {
		return "", err
	}
	if lastHash != "" {
		return lastHash, nil
	}
	return readSeedHash(path)
}

func VerifyFile(path string) (VerificationResult, error) {
	result := VerificationResult{
		Path:  path,
		Valid: true,
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		result.Message = "audit file does not exist"
		return result, nil
	}
	if err != nil {
		return VerificationResult{}, err
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		result.Message = "audit file is empty"
		return result, nil
	}

	lines := strings.Split(trimmed, "\n")
	prevHash, err := readSeedHash(path)
	if err != nil {
		return VerificationResult{}, err
	}
	for idx, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return VerificationResult{}, fmt.Errorf("decode audit line %d: %w", idx+1, err)
		}
		if event.PrevHash != prevHash {
			result.Valid = false
			result.FailureLine = idx + 1
			result.Message = fmt.Sprintf("prev_hash mismatch at line %d", idx+1)
			return result, nil
		}
		expectedHash, err := computeEventHash(event)
		if err != nil {
			return VerificationResult{}, fmt.Errorf("hash audit line %d: %w", idx+1, err)
		}
		if event.EventHash != expectedHash {
			result.Valid = false
			result.FailureLine = idx + 1
			result.Message = fmt.Sprintf("event_hash mismatch at line %d", idx+1)
			return result, nil
		}
		prevHash = event.EventHash
		result.EventCount++
		result.LastHash = event.EventHash
	}

	result.Message = "audit chain verified"
	return result, nil
}

func RotateFile(path string, now time.Time) (RotationResult, error) {
	result := RotationResult{Path: path}
	lastHash, err := readLastEventHash(path)
	if err != nil {
		return RotationResult{}, err
	}
	if lastHash == "" {
		result.Message = "audit file has no events to rotate"
		return result, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	archivePath := fmt.Sprintf("%s.%s", path, now.Format("20060102T150405Z"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return RotationResult{}, err
	}
	if err := os.Rename(path, archivePath); err != nil {
		return RotationResult{}, err
	}
	if err := writeSeedHash(path, lastHash); err != nil {
		return RotationResult{}, err
	}
	result.ArchivePath = archivePath
	result.LastHash = lastHash
	result.Rotated = true
	result.Message = "audit file rotated"
	return result, nil
}

func PruneArchives(path string, keepArchives int) (PruneResult, error) {
	result := PruneResult{
		Path:         path,
		KeepArchives: keepArchives,
	}
	if keepArchives < 0 {
		return PruneResult{}, fmt.Errorf("keepArchives must be non-negative")
	}

	archives, err := listArchiveFiles(path)
	if err != nil {
		return PruneResult{}, err
	}
	if len(archives) <= keepArchives {
		result.Retained = len(archives)
		result.Message = "no archives pruned"
		return result, nil
	}

	removeCount := len(archives) - keepArchives
	for _, archivePath := range archives[:removeCount] {
		if err := os.Remove(archivePath); err != nil {
			return PruneResult{}, err
		}
		result.Removed = append(result.Removed, archivePath)
	}
	result.Retained = keepArchives
	result.Message = "audit archives pruned"
	return result, nil
}

func MaintainFile(path string, maxActiveBytes int64, keepArchives int, now time.Time) (MaintenanceResult, error) {
	result := MaintenanceResult{
		Path:         path,
		KeepArchives: keepArchives,
	}
	if maxActiveBytes <= 0 {
		result.Message = "audit maintenance disabled"
		return result, nil
	}

	needsRotation, err := NeedsRotation(path, maxActiveBytes)
	if err != nil {
		return MaintenanceResult{}, err
	}
	if needsRotation {
		rotation, err := RotateFile(path, now)
		if err != nil {
			return MaintenanceResult{}, err
		}
		result.Rotation = rotation
		result.Rotated = rotation.Rotated
		result.ArchivePath = rotation.ArchivePath
		result.LastHash = rotation.LastHash
	}

	prune, err := PruneArchives(path, keepArchives)
	if err != nil {
		return MaintenanceResult{}, err
	}
	result.Prune = prune
	result.Removed = prune.Removed
	result.Retained = prune.Retained
	if result.Rotated {
		result.Message = "audit maintenance rotated and pruned archives"
	} else {
		result.Message = "audit maintenance checked active file and pruned archives"
	}
	return result, nil
}

func statePath(path string) string {
	return path + ".state"
}

func readSeedHash(path string) (string, error) {
	data, err := os.ReadFile(statePath(path))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var payload struct {
		LastHash string `json:"last_hash"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode audit state: %w", err)
	}
	return payload.LastHash, nil
}

func writeSeedHash(path, lastHash string) error {
	payload, err := json.Marshal(map[string]string{"last_hash": lastHash})
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(path), payload, 0o644)
}

func listArchiveFiles(path string) ([]string, error) {
	matches, err := filepath.Glob(path + ".*")
	if err != nil {
		return nil, err
	}
	archives := make([]string, 0, len(matches))
	for _, match := range matches {
		if match == statePath(path) {
			continue
		}
		archives = append(archives, match)
	}
	sort.Strings(archives)
	return archives, nil
}

func NeedsRotation(path string, maxActiveBytes int64) (bool, error) {
	if maxActiveBytes <= 0 {
		return false, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Size() >= maxActiveBytes, nil
}
