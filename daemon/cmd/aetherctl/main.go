// Command aetherctl is the command-line interface for interacting with a
// running aetherd daemon.  It communicates over the Unix domain socket and
// also supports direct SQLite queries when the daemon is not running.
//
// Usage:
//
//	aetherctl status          — daemon health check
//	aetherctl events [-n N]   — list recent events
//	aetherctl tail            — poll and print events continuously
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
	"text/tabwriter"
	"time"

	"github.com/wambozi/aether/internal/event"
	"github.com/wambozi/aether/internal/socket"
	"github.com/wambozi/aether/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "aetherctl:", err)
		os.Exit(1)
	}
}

func run() error {
	socketPath := flag.String("socket", defaultSocketPath(), "aetherd Unix socket path")
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
	default:
		return fmt.Errorf("unknown command %q — run aetherctl -help", cmd)
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

	fmt.Printf("aetherd  status=%v  version=%v\n",
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
	fmt.Fprintln(os.Stderr, "aetherctl tail: polling every 2s (Ctrl-C to stop)…")
	for {
		_ = cmdEvents(socketPath, dbPath, nil)
		time.Sleep(2 * time.Second)
	}
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
	return filepath.Join(runtime, "aetherd.sock")
}

func defaultDBPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".local", "share")
	}
	return filepath.Join(base, "aetherd", "data.db")
}

func printUsage() {
	fmt.Print(`aetherctl — Aether OS daemon CLI

Commands:
  status              Show daemon health and version
  events [-n N]       List the N most recent collected events (default 20)
  tail                Poll and stream live events every 2s

Flags:
  -socket PATH        Unix socket path (default: $XDG_RUNTIME_DIR/aetherd.sock)
  -db PATH            SQLite path for offline reads
`)
}
