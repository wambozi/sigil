package main

// Notifier is the interface for platform-native desktop notifications.
// Each platform (macOS, Linux, Windows) provides its own implementation.
type Notifier interface {
	// Show displays a desktop notification with the given title, body, and icon.
	// suggestionID is passed so click handlers can navigate to the detail view.
	Show(title, body, iconPath string, suggestionID int64) error

	// Close releases any resources held by the notifier.
	Close()
}
