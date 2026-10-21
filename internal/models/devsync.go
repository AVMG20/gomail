package models

import "time"

// DeviceSyncStatus describes syncing settings, labels and sign-ins between
// computers through a gomail-sync server (Settings → Sync).
type DeviceSyncStatus struct {
	Available      bool       `json:"available"` // false in demo mode
	Connected      bool       `json:"connected"`
	ServerURL      string     `json:"serverUrl"`
	Username       string     `json:"username"`
	IncludeSecrets bool       `json:"includeSecrets"` // sign-ins and API keys sync too
	Syncing        bool       `json:"syncing"`
	LastSyncAt     *time.Time `json:"lastSyncAt"`
	Error          string     `json:"error"` // the last round's problem ("" = fine)
	// DefaultServerURL is the server suggested when connecting.
	DefaultServerURL string `json:"defaultServerUrl"`
}

// DeviceSyncConnect is what the user enters to connect a computer.
type DeviceSyncConnect struct {
	ServerURL  string `json:"serverUrl"` // "" = the default server
	Username   string `json:"username"`
	Passphrase string `json:"passphrase"`
	// Create makes a new account on the server (the first computer);
	// otherwise the computer joins an existing one and takes its settings.
	Create     bool   `json:"create"`
	SignupCode string `json:"signupCode"` // for servers that need one for new accounts
	// IncludeSecrets syncs sign-ins and API keys (a new account; a joining
	// computer follows the account's choice).
	IncludeSecrets bool `json:"includeSecrets"`
}

// EventDeviceSync carries a DeviceSyncStatus whenever it changes.
const EventDeviceSync = "devicesync:status"
