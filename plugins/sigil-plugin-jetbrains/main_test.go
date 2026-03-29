package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPluginEventJSONSerialization(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	event := PluginEvent{
		Plugin:    "jetbrains",
		Kind:      "ide_status",
		Timestamp: now,
		Correlation: map[string]any{
			"repo_root": "/home/user/project",
		},
		Payload: map[string]any{
			"ide":            "GoLand",
			"running":        true,
			"active_project": "/home/user/project",
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal PluginEvent: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded["plugin"] != "jetbrains" {
		t.Errorf("expected plugin=jetbrains, got %v", decoded["plugin"])
	}
	if decoded["kind"] != "ide_status" {
		t.Errorf("expected kind=ide_status, got %v", decoded["kind"])
	}

	payload := decoded["payload"].(map[string]any)
	if payload["ide"] != "GoLand" {
		t.Errorf("expected ide=GoLand, got %v", payload["ide"])
	}
}

func TestParseIDEDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		input           string
		expectedProduct string
		expectedVersion string
	}{
		{
			name:            "PyCharm with version",
			input:           "PyCharm2024.3",
			expectedProduct: "PyCharm",
			expectedVersion: "2024.3",
		},
		{
			name:            "GoLand with version",
			input:           "GoLand2025.1",
			expectedProduct: "GoLand",
			expectedVersion: "2025.1",
		},
		{
			name:            "IntelliJ IDEA",
			input:           "IntelliJIdea2024.2",
			expectedProduct: "IntelliJIdea",
			expectedVersion: "2024.2",
		},
		{
			name:            "no version",
			input:           "PyCharm",
			expectedProduct: "PyCharm",
			expectedVersion: "",
		},
		{
			name:            "RustRover",
			input:           "RustRover2025.1",
			expectedProduct: "RustRover",
			expectedVersion: "2025.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			product, version := parseIDEDir(tc.input)
			if product != tc.expectedProduct {
				t.Errorf("parseIDEDir(%q) product = %q, want %q", tc.input, product, tc.expectedProduct)
			}
			if version != tc.expectedVersion {
				t.Errorf("parseIDEDir(%q) version = %q, want %q", tc.input, version, tc.expectedVersion)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "with USER_HOME placeholder",
			input:    "$USER_HOME$/projects/myapp",
			expected: filepath.Join(home, "/projects/myapp"),
		},
		{
			name:     "without placeholder",
			input:    "/opt/projects/myapp",
			expected: "/opt/projects/myapp",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "just the placeholder",
			input:    "$USER_HOME$",
			expected: home,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := expandHome(tc.input)
			if result != tc.expected {
				t.Errorf("expandHome(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestProductsMapKnownIDEs(t *testing.T) {
	t.Parallel()

	expected := []string{"PyCharm", "GoLand", "IntelliJIdea", "WebStorm", "DataGrip", "RustRover", "CLion"}
	for _, product := range expected {
		if _, ok := products[product]; !ok {
			t.Errorf("expected product %q in products map", product)
		}
	}
}

func TestCLIBinariesMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		product  string
		expected string
	}{
		{"PyCharm", "pycharm"},
		{"GoLand", "goland"},
		{"IntelliJIdea", "idea"},
		{"WebStorm", "webstorm"},
		{"CLion", "clion"},
	}

	for _, tc := range tests {
		bin, ok := cliBinaries[tc.product]
		if !ok {
			t.Errorf("cliBinaries missing %q", tc.product)
			continue
		}
		if bin != tc.expected {
			t.Errorf("cliBinaries[%q] = %q, want %q", tc.product, bin, tc.expected)
		}
	}
}

func TestReadRecentProjectsXML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	optionsDir := filepath.Join(dir, "options")
	if err := os.MkdirAll(optionsDir, 0o755); err != nil {
		t.Fatalf("mkdir options: %v", err)
	}

	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<application>
  <component name="RecentProjectsManager">
    <option name="additionalInfo">
      <map>
        <entry key="$USER_HOME$/projects/alpha">
          <value>
            <RecentProjectMetaInfo>
              <option name="activationTimestamp" value="1700000000000" />
              <option name="projectOpenTimestamp" value="1699999000000" />
              <option name="build" value="GO-243.12345" />
              <option name="productionCode" value="GO" />
            </RecentProjectMetaInfo>
          </value>
        </entry>
        <entry key="$USER_HOME$/projects/beta">
          <value>
            <RecentProjectMetaInfo>
              <option name="activationTimestamp" value="1700001000000" />
              <option name="projectOpenTimestamp" value="1699998000000" />
            </RecentProjectMetaInfo>
          </value>
        </entry>
      </map>
    </option>
  </component>
</application>`

	if err := os.WriteFile(filepath.Join(optionsDir, "recentProjects.xml"), []byte(xmlContent), 0o644); err != nil {
		t.Fatalf("write recentProjects.xml: %v", err)
	}

	projects := readRecentProjects(dir)

	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}

	// Should be sorted by activation time descending.
	if projects[0].path != "$USER_HOME$/projects/beta" {
		t.Errorf("expected first project to be beta (higher TS), got %q", projects[0].path)
	}
	if projects[0].activationTS != 1700001000000 {
		t.Errorf("expected activationTS=1700001000000, got %d", projects[0].activationTS)
	}

	if projects[1].path != "$USER_HOME$/projects/alpha" {
		t.Errorf("expected second project to be alpha, got %q", projects[1].path)
	}
	if projects[1].build != "GO-243.12345" {
		t.Errorf("expected build=GO-243.12345, got %q", projects[1].build)
	}
	if projects[1].productionCode != "GO" {
		t.Errorf("expected productionCode=GO, got %q", projects[1].productionCode)
	}
}

func TestReadRecentProjectsMissingFile(t *testing.T) {
	t.Parallel()

	projects := readRecentProjects("/nonexistent/path")
	if len(projects) != 0 {
		t.Errorf("expected 0 projects for missing file, got %d", len(projects))
	}
}

func TestReadRecentProjectsInvalidXML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	optionsDir := filepath.Join(dir, "options")
	os.MkdirAll(optionsDir, 0o755)
	os.WriteFile(filepath.Join(optionsDir, "recentProjects.xml"), []byte("not xml at all"), 0o644)

	projects := readRecentProjects(dir)
	if len(projects) != 0 {
		t.Errorf("expected 0 projects for invalid XML, got %d", len(projects))
	}
}

func TestXMLApplicationUnmarshal(t *testing.T) {
	t.Parallel()

	xmlContent := `<application>
  <component name="RecentProjectsManager">
    <option name="additionalInfo">
      <map>
        <entry key="/path/to/project">
          <value>
            <RecentProjectMetaInfo>
              <option name="activationTimestamp" value="12345" />
            </RecentProjectMetaInfo>
          </value>
        </entry>
      </map>
    </option>
  </component>
</application>`

	var app xmlApplication
	if err := xml.Unmarshal([]byte(xmlContent), &app); err != nil {
		t.Fatalf("unmarshal XML: %v", err)
	}

	if len(app.Components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(app.Components))
	}
	if app.Components[0].Name != "RecentProjectsManager" {
		t.Errorf("expected component name=RecentProjectsManager, got %q", app.Components[0].Name)
	}
	if len(app.Components[0].Options) != 1 {
		t.Fatalf("expected 1 option, got %d", len(app.Components[0].Options))
	}

	opt := app.Components[0].Options[0]
	if opt.Name != "additionalInfo" {
		t.Errorf("expected option name=additionalInfo, got %q", opt.Name)
	}
	if opt.Map == nil {
		t.Fatal("expected map to be non-nil")
	}
	if len(opt.Map.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(opt.Map.Entries))
	}
	if opt.Map.Entries[0].Key != "/path/to/project" {
		t.Errorf("expected key=/path/to/project, got %q", opt.Map.Entries[0].Key)
	}
}

func TestIDEStateTracking(t *testing.T) {
	t.Parallel()

	state := &ideState{
		activeProject: "/home/user/project-a",
		activationTS:  1700000000000,
	}

	if state.activeProject != "/home/user/project-a" {
		t.Errorf("unexpected activeProject: %q", state.activeProject)
	}
	if state.activationTS != 1700000000000 {
		t.Errorf("unexpected activationTS: %d", state.activationTS)
	}
}
