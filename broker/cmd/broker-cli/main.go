package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/audit"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	baseURL := envOrDefault("BROKER_BASE_URL", "http://127.0.0.1:8081")
	client := &http.Client{}

	switch os.Args[1] {
	case "submit":
		submitCmd(client, baseURL, os.Args[2:])
	case "get":
		getCmd(client, baseURL, os.Args[2:])
	case "root":
		rootCmd(client, baseURL, os.Args[2:])
	case "watch":
		watchCmd(client, baseURL, os.Args[2:])
	case "result":
		resultCmd(client, baseURL, os.Args[2:])
	case "cancel":
		cancelCmd(client, baseURL, os.Args[2:])
	case "verify-audit":
		verifyAuditCmd(os.Args[2:])
	case "rotate-audit":
		rotateAuditCmd(os.Args[2:])
	case "prune-audit":
		pruneAuditCmd(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func submitCmd(client *http.Client, baseURL string, args []string) {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	taskType := fs.String("task-type", "", "Task type, for example document_summary or log_analysis")
	inputURI := fs.String("input-uri", "", "Input file URI, for example file:///tmp/doc.txt")
	schemaName := fs.String("schema", "", "Output schema name")
	classification := fs.String("classification", "", "Optional input classification")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if *taskType == "" || *inputURI == "" || *schemaName == "" {
		fmt.Fprintln(os.Stderr, "submit requires --task-type, --input-uri, and --schema")
		fs.Usage()
		os.Exit(2)
	}

	reqBody := types.SubmitJobRequest{
		TaskType: *taskType,
		InputRefs: []types.InputRef{
			{
				Type: "file",
				URI:  *inputURI,
			},
		},
		OutputSchema: types.OutputSchemaRef{Name: *schemaName},
	}
	if *classification != "" {
		reqBody.TaskParams = map[string]any{
			"classification": *classification,
		}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		fatalf("marshal request: %v", err)
	}

	respBody := doJSON(client, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/jobs", payload)
	fmt.Println(respBody)
}

func getCmd(client *http.Client, baseURL string, args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "get requires <job-id>")
		os.Exit(2)
	}
	respBody := doJSON(client, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/jobs/"+args[0], nil)
	fmt.Println(respBody)
}

func watchCmd(client *http.Client, baseURL string, args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	interval := fs.Duration("interval", time.Second, "Polling interval")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "watch requires <job-id>")
		os.Exit(2)
	}

	jobID := fs.Arg(0)
	baseURL = strings.TrimRight(baseURL, "/")
	lastLine := ""

	for {
		respBody := doJSON(client, http.MethodGet, baseURL+"/v1/jobs/"+jobID, nil)
		var job types.Job
		if err := json.Unmarshal([]byte(respBody), &job); err != nil {
			fatalf("decode job response: %v", err)
		}

		line := formatWatchLine(job)
		if line != lastLine {
			fmt.Println(line)
			lastLine = line
		}

		if isTerminalState(job.State) {
			return
		}
		time.Sleep(*interval)
	}
}

func rootCmd(client *http.Client, baseURL string, args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "root requires <root-job-id>")
		os.Exit(2)
	}
	respBody := doJSON(client, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/roots/"+args[0], nil)
	fmt.Println(respBody)
}

func resultCmd(client *http.Client, baseURL string, args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "result requires <job-id>")
		os.Exit(2)
	}
	respBody := doJSON(client, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/jobs/"+args[0]+"/result", nil)
	fmt.Println(respBody)
}

func cancelCmd(client *http.Client, baseURL string, args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "cancel requires <job-id>")
		os.Exit(2)
	}
	respBody := doJSON(client, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/jobs/"+args[0]+":cancel", nil)
	fmt.Println(respBody)
}

func verifyAuditCmd(args []string) {
	fs := flag.NewFlagSet("verify-audit", flag.ExitOnError)
	path := fs.String("path", envOrDefault("BROKER_AUDIT_LOG_PATH", ".broker/audit.jsonl"), "Path to audit JSONL file")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	result, err := audit.VerifyFile(*path)
	if err != nil {
		fatalf("verify audit file: %v", err)
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatalf("marshal verification result: %v", err)
	}
	fmt.Println(string(body))
	if !result.Valid {
		os.Exit(1)
	}
}

func rotateAuditCmd(args []string) {
	fs := flag.NewFlagSet("rotate-audit", flag.ExitOnError)
	path := fs.String("path", envOrDefault("BROKER_AUDIT_LOG_PATH", ".broker/audit.jsonl"), "Path to audit JSONL file")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	result, err := audit.RotateFile(*path, time.Now().UTC())
	if err != nil {
		fatalf("rotate audit file: %v", err)
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatalf("marshal rotation result: %v", err)
	}
	fmt.Println(string(body))
}

func pruneAuditCmd(args []string) {
	fs := flag.NewFlagSet("prune-audit", flag.ExitOnError)
	path := fs.String("path", envOrDefault("BROKER_AUDIT_LOG_PATH", ".broker/audit.jsonl"), "Path to audit JSONL file")
	keep := fs.Int("keep", 10, "Number of rotated audit segments to retain")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	result, err := audit.PruneArchives(*path, *keep)
	if err != nil {
		fatalf("prune audit archives: %v", err)
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatalf("marshal prune result: %v", err)
	}
	fmt.Println(string(body))
}

func doJSON(client *http.Client, method, url string, payload []byte) string {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		fatalf("build request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fatalf("read response: %v", err)
	}
	if resp.StatusCode >= 300 {
		fatalf("request failed with %s: %s", resp.Status, strings.TrimSpace(string(respBytes)))
	}
	return string(respBytes)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func formatWatchLine(job types.Job) string {
	parts := []string{
		"job=" + job.ID,
		"state=" + string(job.State),
	}
	if job.Progress != nil {
		if job.Progress.Phase != "" {
			parts = append(parts, "phase="+job.Progress.Phase)
		}
		if job.Progress.Percent > 0 {
			parts = append(parts, fmt.Sprintf("percent=%d", job.Progress.Percent))
		}
		if job.Progress.Message != "" {
			parts = append(parts, "message="+quoteIfNeeded(job.Progress.Message))
		}
	}
	if job.BackendState != "" {
		parts = append(parts, "backend="+job.BackendState)
	}
	if job.ResultError != "" {
		parts = append(parts, "error="+quoteIfNeeded(job.ResultError))
	}
	return strings.Join(parts, " ")
}

func quoteIfNeeded(text string) string {
	if strings.ContainsAny(text, " \t") {
		return strconvQuote(text)
	}
	return text
}

func strconvQuote(text string) string {
	payload, _ := json.Marshal(text)
	return string(payload)
}

func isTerminalState(state types.JobState) bool {
	switch state {
	case types.JobStateSucceeded, types.JobStateFailed, types.JobStateCancelled, types.JobStatePreempted, types.JobStateTimedOut:
		return true
	default:
		return false
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `broker-cli

Usage:
  broker-cli submit --task-type <type> --input-uri <file://...> --schema <schema>
  broker-cli get <job-id>
  broker-cli root <root-job-id>
  broker-cli watch [--interval 1s] <job-id>
  broker-cli result <job-id>
  broker-cli cancel <job-id>
  broker-cli verify-audit [--path .broker/audit.jsonl]
  broker-cli rotate-audit [--path .broker/audit.jsonl]
  broker-cli prune-audit [--path .broker/audit.jsonl] [--keep 10]

Environment:
  BROKER_BASE_URL  Broker base URL (default: http://127.0.0.1:8081)
`)
}
