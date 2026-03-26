package sources

import (
	"testing"
)

func TestParseWindowState(t *testing.T) {
	tests := []struct {
		name    string
		proc    string
		title   string
		wantNil bool
		wantApp string
		wantKey string // which key to check for the document/workbook/etc.
		wantVal string // expected value of that key
	}{
		{
			name:    "Excel workbook",
			proc:    "EXCEL.EXE",
			title:   "Q4_Board.xlsx - Excel",
			wantApp: "Microsoft Excel",
			wantKey: "workbook",
			wantVal: "Q4_Board.xlsx",
		},
		{
			name:    "Excel read-only workbook",
			proc:    "EXCEL.EXE",
			title:   "Budget.xlsx [Read-Only] - Excel",
			wantApp: "Microsoft Excel",
			wantKey: "workbook",
			wantVal: "Budget.xlsx",
		},
		{
			name:    "Word document",
			proc:    "WINWORD.EXE",
			title:   "Report.docx - Word",
			wantApp: "Microsoft Word",
			wantKey: "document",
			wantVal: "Report.docx",
		},
		{
			name:    "PowerPoint presentation",
			proc:    "POWERPNT.EXE",
			title:   "Deck.pptx - PowerPoint",
			wantApp: "Microsoft PowerPoint",
			wantKey: "presentation",
			wantVal: "Deck.pptx",
		},
		{
			name:    "Outlook inbox",
			proc:    "OUTLOOK.EXE",
			title:   "Inbox - user@company.com - Outlook",
			wantApp: "Microsoft Outlook",
			wantKey: "folder_or_subject",
			wantVal: "Inbox",
		},
		{
			name:    "Outlook composing HTML",
			proc:    "OUTLOOK.EXE",
			title:   "RE: Meeting - Message (HTML)",
			wantApp: "Microsoft Outlook",
			wantKey: "composing",
			wantVal: "true",
		},
		{
			name:    "Outlook composing plain text",
			proc:    "OUTLOOK.EXE",
			title:   "FW: Notes - Message (Plain Text)",
			wantApp: "Microsoft Outlook",
			wantKey: "composing",
			wantVal: "true",
		},
		{
			name:    "Outlook not composing",
			proc:    "OUTLOOK.EXE",
			title:   "Calendar - user@company.com - Outlook",
			wantApp: "Microsoft Outlook",
			wantKey: "composing",
			wantVal: "false",
		},
		{
			name:    "Untracked app returns nil",
			proc:    "chrome.exe",
			title:   "Google - Chrome",
			wantNil: true,
		},
		{
			name:    "Excel lowercase proc name",
			proc:    "excel.exe",
			title:   "Sheet1.xlsx - Excel",
			wantApp: "Microsoft Excel",
			wantKey: "workbook",
			wantVal: "Sheet1.xlsx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWindowState(tt.proc, tt.title)

			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}

			if got == nil {
				t.Fatal("expected non-nil result, got nil")
			}

			app, _ := got["app"].(string)
			if app != tt.wantApp {
				t.Errorf("app = %q, want %q", app, tt.wantApp)
			}

			val, ok := got[tt.wantKey]
			if !ok {
				t.Fatalf("missing key %q in result %v", tt.wantKey, got)
			}

			// Handle bool values for composing field.
			var valStr string
			switch v := val.(type) {
			case string:
				valStr = v
			case bool:
				if v {
					valStr = "true"
				} else {
					valStr = "false"
				}
			}

			if valStr != tt.wantVal {
				t.Errorf("%s = %q, want %q", tt.wantKey, valStr, tt.wantVal)
			}
		})
	}
}
