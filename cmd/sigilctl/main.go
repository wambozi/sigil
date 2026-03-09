// Command sigilctl is the command-line interface for interacting with a
// running sigild daemon.  It communicates over the Unix domain socket and
// also supports direct SQLite queries when the daemon is not running.
//
// Usage:
//
//	sigilctl status                        — daemon health check
//	sigilctl events [-n N] [-offline]      — list recent events
//	sigilctl tail                          — poll and print events continuously
//	sigilctl files                         — top files by edit count today
//	sigilctl commands                      — command frequency table today
//	sigilctl patterns                      — detected patterns with confidence
//	sigilctl suggestions                   — suggestion history with status
//	sigilctl summary                       — trigger an immediate analysis cycle
//	sigilctl level                         — show current notification level
//	sigilctl level N                       — set notification level (0-4)
//	sigilctl feedback <id> accept|dismiss  — respond to a suggestion
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/inference"
	"github.com/wambozi/sigil/internal/socket"
	"github.com/wambozi/sigil/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sigilctl:", err)
		os.Exit(1)
	}
}

func run() error {
	socketPath := flag.String("socket", defaultSocketPath(), "sigild Unix socket path")
	dbPath := flag.String("db", defaultDBPath(), "SQLite database path (used when daemon is offline)")
	flag.Parse()

	if flag.NArg() == 0 {
		printUsage()
		return nil
	}

	cmd, args := flag.Arg(0), flag.Args()[1:]

	switch cmd {
	case "status":
		return cmdStatus(*socketPath)
	case "events":
		return cmdEvents(*socketPath, *dbPath, args)
	case "tail":
		return cmdTail(*socketPath, *dbPath)
	case "files":
		return cmdFiles(*socketPath)
	case "commands":
		return cmdCommands(*socketPath)
	case "patterns":
		return cmdPatterns(*socketPath)
	case "suggestions":
		return cmdSuggestions(*socketPath)
	case "summary":
		return cmdSummary(*socketPath)
	case "level":
		return cmdLevel(*socketPath, args)
	case "feedback":
		return cmdFeedback(*socketPath, args)
	case "config":
		return cmdConfig(*socketPath)
	case "actions":
		return cmdActions(*socketPath)
	case "purge":
		return cmdPurge(*dbPath)
	case "export":
		return cmdExport(*dbPath)
	case "model":
		return cmdModel(args)
	case "fleet":
		return cmdFleet(*socketPath, args)
	default:
		return fmt.Errorf("unknown command %q — run sigilctl -help", cmd)
	}
}

// --- Commands ---------------------------------------------------------------

func cmdStatus(socketPath string) error {
	resp, err := call(socketPath, "status", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload map[string]any
	_ = json.Unmarshal(resp.Payload, &payload)

	fmt.Printf("sigild  status=%v  version=%v\n",
		payload["status"], payload["version"])
	return nil
}

func cmdEvents(socketPath, dbPath string, args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	n := fs.Int("n", 20, "number of events to show")
	offline := fs.Bool("offline", false, "read directly from SQLite (bypasses daemon)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *offline {
		return eventsFromDB(dbPath, *n)
	}

	resp, err := call(socketPath, "events", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: daemon unreachable, falling back to direct DB read")
		return eventsFromDB(dbPath, *n)
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	return printEventsJSON(resp.Payload, *n)
}

// cmdTail polls the events endpoint every two seconds and prints new entries.
// Phase 2 will replace this with a proper push subscription over the socket.
func cmdTail(socketPath, dbPath string) error {
	fmt.Fprintln(os.Stderr, "sigilctl tail: polling every 2s (Ctrl-C to stop)...")
	for {
		_ = cmdEvents(socketPath, dbPath, nil)
		time.Sleep(2 * time.Second)
	}
}

// cmdFiles prints the top edited files from the last 24 hours.
func cmdFiles(socketPath string) error {
	resp, err := call(socketPath, "files", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var files []struct {
		Path  string `json:"Path"`
		Count int64  `json:"Count"`
	}
	if err := json.Unmarshal(resp.Payload, &files); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FILE\tEDITS")
	for _, f := range files {
		fmt.Fprintf(w, "%s\t%d\n", f.Path, f.Count)
	}
	return w.Flush()
}

// cmdCommands prints the command frequency table for the last 24 hours.
func cmdCommands(socketPath string) error {
	resp, err := call(socketPath, "commands", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var rows []struct {
		Cmd          string `json:"cmd"`
		Count        int    `json:"count"`
		LastExitCode int    `json:"last_exit_code"`
	}
	if err := json.Unmarshal(resp.Payload, &rows); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "COMMAND\tCOUNT\tLAST EXIT")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%d\t%d\n", r.Cmd, r.Count, r.LastExitCode)
	}
	return w.Flush()
}

// cmdPatterns prints detected patterns with their confidence scores.
func cmdPatterns(socketPath string) error {
	resp, err := call(socketPath, "patterns", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var patterns []struct {
		ID         int64   `json:"id"`
		Category   string  `json:"category"`
		Confidence float64 `json:"confidence"`
		Title      string  `json:"title"`
		Body       string  `json:"body"`
	}
	if err := json.Unmarshal(resp.Payload, &patterns); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(patterns) == 0 {
		fmt.Println("No patterns detected yet.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATTERN\tCONFIDENCE\tBODY")
	for _, p := range patterns {
		fmt.Fprintf(w, "%s\t%.2f\t%s\n", p.Title, p.Confidence, p.Body)
	}
	return w.Flush()
}

// cmdSuggestions prints the suggestion history with lifecycle status.
func cmdSuggestions(socketPath string) error {
	resp, err := call(socketPath, "suggestions", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var suggestions []struct {
		ID         int64   `json:"id"`
		Status     string  `json:"status"`
		Confidence float64 `json:"confidence"`
		Title      string  `json:"title"`
		Body       string  `json:"body"`
	}
	if err := json.Unmarshal(resp.Payload, &suggestions); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(suggestions) == 0 {
		fmt.Println("No suggestions recorded yet.")
		return nil
	}

	for _, s := range suggestions {
		fmt.Printf("[%d] %s (%.2f) — %s\n", s.ID, s.Status, s.Confidence, s.Title)
		if s.Body != "" {
			fmt.Printf("    %s\n", s.Body)
		}
		fmt.Println()
	}
	return nil
}

// cmdSummary triggers an immediate analysis cycle in the daemon.
func cmdSummary(socketPath string) error {
	resp, err := call(socketPath, "trigger-summary", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload map[string]any
	_ = json.Unmarshal(resp.Payload, &payload)
	fmt.Printf("sigild: %v\n", payload["message"])
	return nil
}

// cmdLevel shows or sets the notifier level.
//
// With no args it reads the current level from the status endpoint.
// With a single numeric arg it sets the level via set-level.
func cmdLevel(socketPath string, args []string) error {
	if len(args) == 0 {
		return showLevel(socketPath)
	}
	return setLevel(socketPath, args[0])
}

func showLevel(socketPath string) error {
	resp, err := call(socketPath, "status", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload map[string]any
	_ = json.Unmarshal(resp.Payload, &payload)

	level, _ := payload["notifier_level"].(float64)
	fmt.Printf("Notification level: %d (%s)\n", int(level), levelName(int(level)))
	return nil
}

func setLevel(socketPath, arg string) error {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 0 || n > 4 {
		return fmt.Errorf("level must be an integer 0-4, got %q", arg)
	}

	resp, err := call(socketPath, "set-level", map[string]any{"level": n})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	fmt.Printf("Notification level set to %d (%s)\n", n, levelName(n))
	return nil
}

// levelName returns the human-readable name for a notifier level integer.
func levelName(n int) string {
	switch n {
	case 0:
		return "silent"
	case 1:
		return "digest"
	case 2:
		return "ambient"
	case 3:
		return "conversational"
	case 4:
		return "autonomous"
	default:
		return "unknown"
	}
}

// cmdFeedback records an explicit accept or dismiss outcome for a suggestion.
// Usage: sigilctl feedback <id> accept|dismiss
func cmdFeedback(socketPath string, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: sigilctl feedback <id> accept|dismiss")
	}

	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("id must be a positive integer, got %q", args[0])
	}

	outcome := args[1]
	switch outcome {
	case "accept":
		outcome = "accepted"
	case "dismiss":
		outcome = "dismissed"
	case "accepted", "dismissed":
		// already canonical
	default:
		return fmt.Errorf("outcome must be accept or dismiss, got %q", args[1])
	}

	resp, err := call(socketPath, "feedback", map[string]any{
		"suggestion_id": id,
		"outcome":       outcome,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	fmt.Printf("Suggestion %d marked %s.\n", id, outcome)
	return nil
}

// --- Socket helpers ---------------------------------------------------------

func call(socketPath, method string, payload any) (socket.Response, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return socket.Response{}, fmt.Errorf("connect to daemon at %s: %w", socketPath, err)
	}
	defer conn.Close()

	req := socket.Request{Method: method}
	if payload != nil {
		req.Payload, _ = json.Marshal(payload)
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return socket.Response{}, fmt.Errorf("send request: %w", err)
	}

	var resp socket.Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return socket.Response{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}

// --- Store helpers ----------------------------------------------------------

func eventsFromDB(dbPath string, n int) error {
	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	ctx := context.Background()
	events, err := db.QueryEvents(ctx, "", n)
	if err != nil {
		return err
	}
	return printEvents(events)
}

func printEventsJSON(raw json.RawMessage, n int) error {
	var events []map[string]any
	if err := json.Unmarshal(raw, &events); err != nil {
		return err
	}
	if len(events) > n {
		events = events[:n]
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tSOURCE\tTIMESTAMP")
	for _, e := range events {
		fmt.Fprintf(w, "%v\t%v\t%v\t%v\n",
			e["id"], e["kind"], e["source"], e["timestamp"])
	}
	return w.Flush()
}

func printEvents(events []event.Event) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tSOURCE\tTIMESTAMP")
	for _, e := range events {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n",
			e.ID, e.Kind, e.Source, e.Timestamp.Format(time.RFC3339))
	}
	return w.Flush()
}

// --- Path helpers -----------------------------------------------------------

func defaultSocketPath() string {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(runtime, "sigild.sock")
}

func defaultDBPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".local", "share")
	}
	return filepath.Join(base, "sigild", "data.db")
}

// cmdActions prints recent undoable actions.
func cmdActions(socketPath string) error {
	resp, err := call(socketPath, "actions", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var actions []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		UndoCmd     string `json:"undo_cmd"`
		ExpiresAt   string `json:"expires_at"`
	}
	if err := json.Unmarshal(resp.Payload, &actions); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(actions) == 0 {
		fmt.Println("No undoable actions.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDESCRIPTION\tUNDO CMD\tEXPIRES")
	for _, a := range actions {
		undoLabel := a.UndoCmd
		if undoLabel == "" {
			undoLabel = "(irreversible)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.ID, a.Description, undoLabel, a.ExpiresAt)
	}
	return w.Flush()
}

// cmdConfig fetches the resolved daemon configuration and prints it as a
// key = value table.
func cmdConfig(socketPath string) error {
	resp, err := call(socketPath, "config", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	order := []string{
		"db_path", "socket_path",
		"inference_mode",
		"watch_paths", "repo_paths",
		"analyze_every", "notifier_level", "log_level", "digest_time",
		"raw_event_days",
	}
	for _, k := range order {
		v, ok := payload[k]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "%s\t= %v\n", k, v)
	}
	return w.Flush()
}

// cmdPurge prompts for confirmation and deletes all local data directly from
// SQLite (works without a running daemon).
func cmdPurge(dbPath string) error {
	fmt.Fprint(os.Stdout, "This will delete all local data. Type 'yes' to confirm: ")
	var answer string
	if _, err := fmt.Fscan(os.Stdin, &answer); err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}
	if answer != "yes" {
		fmt.Println("Aborted.")
		return nil
	}

	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	if err := db.Purge(); err != nil {
		return fmt.Errorf("purge: %w", err)
	}
	fmt.Println("All local data deleted.")
	return nil
}

// cmdExport writes all events and suggestions as newline-delimited JSON to
// stdout.  Works without a running daemon (direct DB access).
func cmdExport(dbPath string) error {
	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	return db.Export(os.Stdout)
}

// cmdModel handles model subcommands: pull, list, path.
func cmdModel(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl model <pull|list|path> [name]")
		return nil
	}
	switch args[0] {
	case "pull":
		return cmdModelPull(args[1:])
	case "list":
		return cmdModelList()
	case "path":
		return cmdModelPath(args[1:])
	default:
		return fmt.Errorf("unknown model command %q — use pull, list, or path", args[0])
	}
}

func cmdModelPull(args []string) error {
	name := inference.DefaultModel
	if len(args) > 0 {
		name = args[0]
	}
	_, err := inference.EnsureModel(context.Background(), name, os.Stdout)
	return err
}

func cmdModelList() error {
	cached := inference.ListCachedModels()
	if len(cached) == 0 {
		fmt.Println("No cached models. Run 'sigilctl model pull' to download one.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE (MB)\tPATH")
	for _, m := range cached {
		fmt.Fprintf(w, "%s\t%d\t%s\n", m.Name, m.Size/(1024*1024), m.Path)
	}
	return w.Flush()
}

func cmdModelPath(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sigilctl model path <name>")
	}
	p := inference.ModelPath(args[0])
	if p == "" {
		return fmt.Errorf("model %q not found locally", args[0])
	}
	fmt.Println(p)
	return nil
}

// cmdFleet handles fleet subcommands: status, preview, opt-out.
func cmdFleet(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl fleet <status|preview|opt-out>")
		return nil
	}
	switch args[0] {
	case "status":
		return cmdFleetStatus(socketPath)
	case "preview":
		return cmdFleetPreview(socketPath)
	case "opt-out":
		return cmdFleetOptOut(socketPath)
	default:
		return fmt.Errorf("unknown fleet command %q — use status, preview, or opt-out", args[0])
	}
}

func cmdFleetStatus(socketPath string) error {
	resp, err := call(socketPath, "fleet-preview", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		fmt.Println("Fleet reporting: disabled")
		return nil
	}
	fmt.Println("Fleet reporting: enabled")
	var report map[string]any
	_ = json.Unmarshal(resp.Payload, &report)
	fmt.Printf("  Node ID: %v\n", report["node_id"])
	fmt.Printf("  Adoption tier: %v\n", report["adoption_tier"])
	fmt.Printf("  Total events: %v\n", report["total_events"])
	return nil
}

func cmdFleetPreview(socketPath string) error {
	resp, err := call(socketPath, "fleet-preview", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("fleet preview: %s", resp.Error)
	}
	var pretty json.RawMessage
	if err := json.Unmarshal(resp.Payload, &pretty); err != nil {
		return err
	}
	out, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(out))
	return nil
}

func cmdFleetOptOut(socketPath string) error {
	resp, err := call(socketPath, "fleet-opt-out", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("fleet opt-out: %s", resp.Error)
	}
	fmt.Println("Fleet reporting disabled. Pending queue cleared.")
	return nil
}

func printUsage() {
	fmt.Print(`sigilctl — Sigil OS daemon CLI

Commands:
  status                        Show daemon health and version
  events [-n N] [-offline]      List the N most recent events (default 20)
  tail                          Poll and stream live events every 2s
  files                         Top files by edit count in the last 24h
  commands                      Command frequency table for the last 24h
  patterns                      Detected patterns with confidence scores
  suggestions                   Suggestion history with lifecycle status
  summary                       Trigger an immediate analysis cycle
  level                         Show current notification level
  level N                       Set notification level (0=silent 1=digest
                                2=ambient 3=conversational 4=autonomous)
  feedback <id> accept|dismiss  Respond to a suggestion by ID
  actions                       Show recent undoable actions
  config                        Print resolved daemon configuration
  model pull [name]             Download a model (default: lfm2-24b-a2b-q4_k_m)
  model list                    List locally cached models
  model path <name>             Print path to a cached model
  fleet status                  Show fleet reporting opt-in status
  fleet preview                 Show what fleet data will be sent
  fleet opt-out                 Disable fleet reporting
  purge                         Delete all local data (requires confirmation)
  export                        Export all data as newline-delimited JSON

Flags:
  -socket PATH    Unix socket path (default: $XDG_RUNTIME_DIR/sigild.sock)
  -db PATH        SQLite path for offline reads
`)
}
