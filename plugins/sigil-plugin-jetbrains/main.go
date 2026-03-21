// Command sigil-plugin-jetbrains monitors all JetBrains IDEs (PyCharm, GoLand,
// IntelliJ, WebStorm, DataGrip, RustRover, CLion, etc.) by reading their
// shared config/state files and detecting running processes.
//
// It discovers all installed JetBrains IDEs automatically — one plugin covers
// the entire JetBrains suite.
//
// Data collected:
//   - Which IDEs are running and which project is open in each
//   - Recent project history with open timestamps per IDE
//   - Active project switches (detected via activation timestamp changes)
//   - Installed plugins per IDE (filtered to interesting ones)
//
// Install: ships with sigild (make build / make install).
package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	defaultIngestURL = "http://127.0.0.1:7775/api/v1/ingest"
	pluginName       = "jetbrains"
)

type PluginEvent struct {
	Plugin      string         `json:"plugin"`
	Kind        string         `json:"kind"`
	Timestamp   time.Time      `json:"timestamp"`
	Correlation map[string]any `json:"correlation,omitempty"`
	Payload     map[string]any `json:"payload"`
}

// Known JetBrains product codes and their display names.
var products = map[string]string{
	"PyCharm":      "PyCharm",
	"GoLand":       "GoLand",
	"IntelliJIdea": "IntelliJ IDEA",
	"WebStorm":     "WebStorm",
	"DataGrip":     "DataGrip",
	"DataSpell":    "DataSpell",
	"RustRover":    "RustRover",
	"CLion":        "CLion",
	"PhpStorm":     "PhpStorm",
	"Rider":        "Rider",
	"Aqua":         "Aqua",
	"Writerside":   "Writerside",
	"Fleet":        "Fleet",
}

// State tracks the last known state per IDE to detect changes.
type ideState struct {
	activeProject string
	activationTS  int64
}

var (
	ingestURL    string
	pollInterval time.Duration
	lastState    = make(map[string]*ideState) // key: "product/version"
)

// CLI binary names for each product (installed by JetBrains Toolbox or manually).
var cliBinaries = map[string]string{
	"PyCharm":      "pycharm",
	"GoLand":       "goland",
	"IntelliJIdea": "idea",
	"WebStorm":     "webstorm",
	"DataGrip":     "datagrip",
	"DataSpell":    "dataspell",
	"RustRover":    "rustrover",
	"CLion":        "clion",
	"PhpStorm":     "phpstorm",
	"Rider":        "rider",
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "capabilities":
			printCapabilities()
			return
		case "open-file":
			cmdOpenFile()
			return
		case "focus":
			cmdFocus()
			return
		}
	}

	flag.StringVar(&ingestURL, "sigil-ingest-url", defaultIngestURL, "Sigil ingest URL")
	flag.DurationVar(&pollInterval, "poll-interval", 30*time.Second, "Poll interval")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "sigil-plugin-jetbrains: polling every %s\n", pollInterval)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	pollAll()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			fmt.Fprintln(os.Stderr, "sigil-plugin-jetbrains: shutting down")
			return
		case <-ticker.C:
			pollAll()
		}
	}
}

func pollAll() {
	ides := discoverIDEs()
	running := detectRunning()

	for _, ide := range ides {
		isRunning := running[ide.product]

		// Read recent projects for this IDE version.
		projects := readRecentProjects(ide.configDir)
		if len(projects) == 0 {
			continue
		}

		// Find the most recently activated project.
		active := projects[0] // sorted by activation time desc

		// Check for project switch.
		stateKey := ide.product + "/" + ide.version
		prev, seen := lastState[stateKey]
		switched := false
		if seen && prev.activeProject != active.path {
			switched = true
		}
		lastState[stateKey] = &ideState{
			activeProject: active.path,
			activationTS:  active.activationTS,
		}

		// Emit IDE status event.
		recentPaths := make([]string, 0, len(projects))
		for _, p := range projects {
			if len(recentPaths) >= 5 {
				break
			}
			recentPaths = append(recentPaths, p.path)
		}

		send(PluginEvent{
			Plugin:    pluginName,
			Kind:      "ide_status",
			Timestamp: time.Now(),
			Correlation: map[string]any{
				"repo_root": expandHome(active.path),
			},
			Payload: map[string]any{
				"ide":             ide.product,
				"ide_display":     products[ide.product],
				"version":         ide.version,
				"running":         isRunning,
				"active_project":  expandHome(active.path),
				"recent_projects": recentPaths,
				"project_count":   len(projects),
			},
		})

		// Emit project switch event if detected.
		if switched {
			send(PluginEvent{
				Plugin:    pluginName,
				Kind:      "project_switch",
				Timestamp: time.Now(),
				Correlation: map[string]any{
					"repo_root": expandHome(active.path),
				},
				Payload: map[string]any{
					"ide":          ide.product,
					"from_project": expandHome(prev.activeProject),
					"to_project":   expandHome(active.path),
				},
			})
		}
	}
}

// --- IDE discovery ---

type ideInstance struct {
	product   string // e.g. "PyCharm"
	version   string // e.g. "2024.3"
	configDir string // full path to config directory
}

func discoverIDEs() []ideInstance {
	baseDir := jetbrainsConfigBase()
	if baseDir == "" {
		return nil
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	var ides []ideInstance
	seen := make(map[string]bool)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Parse "PyCharm2024.3" → product="PyCharm", version="2024.3"
		product, version := parseIDEDir(name)
		if product == "" {
			continue
		}
		if _, known := products[product]; !known {
			continue
		}

		// Keep only the latest version per product.
		if seen[product] {
			continue // entries are sorted alphabetically, last version wins
		}

		configDir := filepath.Join(baseDir, name)
		recentFile := filepath.Join(configDir, "options", "recentProjects.xml")
		if _, err := os.Stat(recentFile); err != nil {
			continue
		}

		ides = append(ides, ideInstance{
			product:   product,
			version:   version,
			configDir: configDir,
		})
		seen[product] = true
	}

	// Reverse so latest versions come first (ReadDir returns alphabetical).
	sort.Slice(ides, func(i, j int) bool {
		if ides[i].product == ides[j].product {
			return ides[i].version > ides[j].version
		}
		return ides[i].product < ides[j].product
	})

	// Dedupe — keep only latest version per product.
	deduped := make([]ideInstance, 0, len(ides))
	seenProducts := make(map[string]bool)
	for _, ide := range ides {
		if seenProducts[ide.product] {
			continue
		}
		seenProducts[ide.product] = true
		deduped = append(deduped, ide)
	}

	return deduped
}

func parseIDEDir(name string) (product, version string) {
	// Find where the version number starts (first digit after letters).
	for i, c := range name {
		if c >= '0' && c <= '9' {
			return name[:i], name[i:]
		}
	}
	return name, ""
}

func jetbrainsConfigBase() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "JetBrains")
	case "linux":
		return filepath.Join(home, ".config", "JetBrains")
	default:
		return ""
	}
}

// --- Recent projects parsing ---

type recentProject struct {
	path           string
	activationTS   int64
	projectOpenTS  int64
	build          string
	productionCode string
}

// XML structures for recentProjects.xml
type xmlApplication struct {
	XMLName    xml.Name       `xml:"application"`
	Components []xmlComponent `xml:"component"`
}

type xmlComponent struct {
	Name    string      `xml:"name,attr"`
	Options []xmlOption `xml:"option"`
}

type xmlOption struct {
	Name  string  `xml:"name,attr"`
	Value string  `xml:"value,attr"`
	Map   *xmlMap `xml:"map"`
}

type xmlMap struct {
	Entries []xmlEntry `xml:"entry"`
}

type xmlEntry struct {
	Key   string `xml:"key,attr"`
	Value struct {
		Meta xmlRecentMeta `xml:"RecentProjectMetaInfo"`
	} `xml:"value"`
}

type xmlRecentMeta struct {
	Options []xmlOption `xml:"option"`
}

func readRecentProjects(configDir string) []recentProject {
	path := filepath.Join(configDir, "options", "recentProjects.xml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var app xmlApplication
	if err := xml.Unmarshal(data, &app); err != nil {
		return nil
	}

	var projects []recentProject

	for _, comp := range app.Components {
		if comp.Name != "RecentProjectsManager" {
			continue
		}
		for _, opt := range comp.Options {
			if opt.Name != "additionalInfo" || opt.Map == nil {
				continue
			}
			for _, entry := range opt.Map.Entries {
				p := recentProject{path: entry.Key}
				for _, metaOpt := range entry.Value.Meta.Options {
					switch metaOpt.Name {
					case "activationTimestamp":
						fmt.Sscanf(metaOpt.Value, "%d", &p.activationTS)
					case "projectOpenTimestamp":
						fmt.Sscanf(metaOpt.Value, "%d", &p.projectOpenTS)
					case "build":
						p.build = metaOpt.Value
					case "productionCode":
						p.productionCode = metaOpt.Value
					}
				}
				projects = append(projects, p)
			}
		}
	}

	// Sort by activation time descending (most recent first).
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].activationTS > projects[j].activationTS
	})

	return projects
}

// --- Running IDE detection ---

func detectRunning() map[string]bool {
	running := make(map[string]bool)

	if runtime.GOOS == "darwin" {
		// Use lsappinfo to detect running JetBrains apps.
		out, err := exec.Command("lsappinfo", "list").Output()
		if err != nil {
			return running
		}
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			lower := strings.ToLower(line)
			for product := range products {
				if strings.Contains(lower, strings.ToLower(product)) {
					running[product] = true
				}
			}
		}
	} else {
		// Linux: check /proc for JetBrains processes.
		out, err := exec.Command("ps", "axo", "comm").Output()
		if err != nil {
			return running
		}
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			lower := strings.ToLower(line)
			for product := range products {
				if strings.Contains(lower, strings.ToLower(product)) {
					running[product] = true
				}
			}
		}
	}

	return running
}

// --- Helpers ---

func expandHome(path string) string {
	if strings.HasPrefix(path, "$USER_HOME$") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[len("$USER_HOME$"):])
	}
	return path
}

func send(event PluginEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(ingestURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	resp.Body.Close()
}

// --- Capabilities ---

func printCapabilities() {
	caps := map[string]any{
		"plugin": pluginName,
		"actions": []map[string]any{
			{
				"name":        "open_file",
				"description": "Open a file in the appropriate JetBrains IDE at a specific line",
				"command":     "sigil-plugin-jetbrains open-file --file <file> [--line <line>] [--ide <ide>]",
			},
			{
				"name":        "focus",
				"description": "Bring a JetBrains IDE to the foreground",
				"command":     "sigil-plugin-jetbrains focus [--ide <ide>]",
			},
		},
		"data_sources": []string{
			"ide_status", "project_switch",
		},
	}
	json.NewEncoder(os.Stdout).Encode(caps)
}

// --- Actions ---

// cmdOpenFile opens a file in the appropriate JetBrains IDE.
// It auto-detects which IDE to use based on the file's project,
// or accepts an explicit --ide flag.
func cmdOpenFile() {
	fs := flag.NewFlagSet("open-file", flag.ExitOnError)
	file := fs.String("file", "", "File path to open")
	line := fs.Int("line", 0, "Line number to navigate to")
	column := fs.Int("column", 1, "Column number")
	ide := fs.String("ide", "", "IDE to use (e.g. GoLand, PyCharm). Auto-detected if not set.")
	fs.Parse(os.Args[2:])

	if *file == "" {
		fmt.Fprintln(os.Stderr, "usage: sigil-plugin-jetbrains open-file --file <path> [--line N] [--ide GoLand]")
		os.Exit(1)
	}

	// Auto-detect the IDE from the file path if not specified.
	targetIDE := *ide
	if targetIDE == "" {
		targetIDE = detectIDEForFile(*file)
	}
	if targetIDE == "" {
		// Fall back to any running IDE.
		running := detectRunning()
		for product := range running {
			targetIDE = product
			break
		}
	}
	if targetIDE == "" {
		fmt.Fprintln(os.Stderr, "sigil-plugin-jetbrains: no IDE detected — specify --ide")
		os.Exit(1)
	}

	bin := findIDEBinary(targetIDE)
	if bin == "" {
		fmt.Fprintf(os.Stderr, "sigil-plugin-jetbrains: CLI binary for %s not found in PATH\n", targetIDE)
		os.Exit(1)
	}

	// Build args: <binary> --line N --column C <file>
	args := []string{}
	if *line > 0 {
		args = append(args, "--line", fmt.Sprintf("%d", *line))
	}
	if *column > 0 {
		args = append(args, "--column", fmt.Sprintf("%d", *column))
	}
	args = append(args, *file)

	fmt.Fprintf(os.Stderr, "sigil-plugin-jetbrains: opening %s in %s (line %d)\n", filepath.Base(*file), targetIDE, *line)

	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sigil-plugin-jetbrains: %v\n", err)
		os.Exit(1)
	}
}

// cmdFocus brings a JetBrains IDE to the foreground.
func cmdFocus() {
	fs := flag.NewFlagSet("focus", flag.ExitOnError)
	ide := fs.String("ide", "", "IDE to focus (e.g. GoLand, PyCharm). Focuses most recent if not set.")
	fs.Parse(os.Args[2:])

	targetIDE := *ide
	if targetIDE == "" {
		// Focus the most recently active IDE.
		running := detectRunning()
		for product := range running {
			targetIDE = product
			break
		}
	}

	if targetIDE == "" {
		fmt.Fprintln(os.Stderr, "sigil-plugin-jetbrains: no running IDE found")
		os.Exit(1)
	}

	displayName := products[targetIDE]
	if displayName == "" {
		displayName = targetIDE
	}

	if runtime.GOOS == "darwin" {
		// Use osascript to activate the app.
		script := fmt.Sprintf(`tell application "%s" to activate`, displayName)
		exec.Command("osascript", "-e", script).Run()
		fmt.Fprintf(os.Stderr, "sigil-plugin-jetbrains: focused %s\n", displayName)
	} else {
		// On Linux, use wmctrl or xdotool if available.
		if wmctrl, err := exec.LookPath("wmctrl"); err == nil {
			exec.Command(wmctrl, "-a", displayName).Run()
		}
	}
}

// detectIDEForFile determines which IDE should open a file based on which
// IDE has the file's project directory as a recent project.
func detectIDEForFile(filePath string) string {
	absPath, _ := filepath.Abs(filePath)

	ides := discoverIDEs()
	for _, ide := range ides {
		projects := readRecentProjects(ide.configDir)
		for _, proj := range projects {
			projPath := expandHome(proj.path)
			if strings.HasPrefix(absPath, projPath) {
				return ide.product
			}
		}
	}
	return ""
}

// findIDEBinary finds the CLI binary for a given IDE product.
func findIDEBinary(product string) string {
	// Check known binary name.
	if bin, ok := cliBinaries[product]; ok {
		if path, err := exec.LookPath(bin); err == nil {
			return path
		}
	}
	// Try lowercase product name.
	lower := strings.ToLower(product)
	if path, err := exec.LookPath(lower); err == nil {
		return path
	}
	return ""
}
