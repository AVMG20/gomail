package models

import "time"

// DeviceSyncStatus describes syncing settings, labels and sign-ins between
// computers through a gomail-sync server (Settings → Sync).
type DeviceSyncStatus struct {
	Available      bool       `json:"available"` // false in demo mode
	Connected      bool       `json:"connected"`
	ServerURL      string     `json:"serverUrl"`
	IncludeSecrets bool       `json:"includeSecrets"` // sign-ins and API keys sync too
	Syncing        bool       `json:"syncing"`
	LastSyncAt     *time.Time `json:"lastSyncAt"`
	Error          string     `json:"error"` // the last round's problem ("" = fine)
	// DefaultServerURL is the server suggested when connecting.
	DefaultServerURL string `json:"defaultServerUrl"`
}

// DeviceSyncConnect turns on sync for a computer.
type DeviceSyncConnect struct {
	ServerURL string `json:"serverUrl"` // "" = the default server
	// Create turns on sync with a new sync code (the first computer; read
	// it with GetDeviceSyncCode). Otherwise Code is the 24-word code of
	// another computer, and this one takes the synced settings.
	Create bool   `json:"create"`
	Code   string `json:"code"`
	// IncludeSecrets syncs sign-ins and API keys (a new account; a joining
	// computer follows the account's choice).
	IncludeSecrets bool `json:"includeSecrets"`
}

// Device sync events.
const (
	EventDeviceSync = "devicesync:status" // DeviceSyncStatus, whenever it changes
	// EventSettingsChanged: another computer changed settings; re-read
	// them all (GetSettings), keeping edits that aren't saved yet.
	EventSettingsChanged = "settings:changed"
)
