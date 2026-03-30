package sources

import "strings"

// parseWindowState extracts app-specific state from process name and window
// title. Office apps include document info in their titles:
//
//	Excel:      "Q4_Board.xlsx - Excel"
//	Word:       "Report.docx - Word"
//	PowerPoint: "Deck.pptx - PowerPoint"
//	Outlook:    "Inbox - user@company.com - Outlook"
//
// Returns nil for processes that are not tracked Office apps.
func parseWindowState(procName, title string) map[string]any {
	procLower := strings.ToLower(procName)

	switch {
	case strings.Contains(procLower, "excel"):
		doc := strings.TrimSuffix(title, " - Excel")
		doc = strings.TrimSuffix(doc, " [Read-Only]")
		doc = strings.TrimSpace(doc)
		return map[string]any{
			"app":      "Microsoft Excel",
			"workbook": doc,
		}

	case strings.Contains(procLower, "winword"):
		doc := strings.TrimSuffix(title, " - Word")
		doc = strings.TrimSpace(doc)
		return map[string]any{
			"app":      "Microsoft Word",
			"document": doc,
		}

	case strings.Contains(procLower, "powerpnt"):
		doc := strings.TrimSuffix(title, " - PowerPoint")
		doc = strings.TrimSpace(doc)
		return map[string]any{
			"app":          "Microsoft PowerPoint",
			"presentation": doc,
		}

	case strings.Contains(procLower, "outlook"):
		parts := strings.SplitN(title, " - ", 3)
		state := map[string]any{"app": "Microsoft Outlook"}
		if len(parts) >= 1 {
			state["folder_or_subject"] = strings.TrimSpace(parts[0])
		}
		state["composing"] = strings.Contains(title, "Message (HTML)") ||
			strings.Contains(title, "Message (Plain Text)")
		return state
	}

	return nil
}
