package models

// BackupResult describes a written backup (ExportBackup).
type BackupResult struct {
	Cancelled        bool   `json:"cancelled"` // the user closed the save dialog
	Path             string `json:"path"`
	Accounts         int    `json:"accounts"`
	Labels           int    `json:"labels"`
	LabelAssignments int    `json:"labelAssignments"` // label marks: manual/AI labels, removals, overrides
	IncludesSecrets  bool   `json:"includesSecrets"`
}

// RestoreSummary describes an imported backup (ImportBackup). Importing
// merges into the current install; nothing is deleted.
type RestoreSummary struct {
	Cancelled bool   `json:"cancelled"` // the user closed the open dialog
	Path      string `json:"path"`
	CreatedAt string `json:"createdAt"` // when the backup was made (RFC 3339)
	Labels    int    `json:"labels"`    // labels in the backup
	Rules     int    `json:"rules"`     // entries of the backup's "...rules" settings lists
	// AccountsAdded were recreated and need a sign-in (Accounts with
	// needsReauth; ReauthAccount finishes them). AccountsReady were
	// recreated and could reuse a sign-in still stored on this device.
	AccountsAdded int `json:"accountsAdded"`
	AccountsReady int `json:"accountsReady"`
	// AccountsMerged already existed here (same provider and email): kept
	// as they are, with the backup's labels and calendar choices merged in.
	AccountsMerged   int  `json:"accountsMerged"`
	AccountsSkipped  int  `json:"accountsSkipped"`  // invalid entries
	LabelAssignments int  `json:"labelAssignments"` // label marks restored
	AppliedNow       int  `json:"appliedNow"`       // of which applied right away (their messages are already here); the rest apply as mail syncs
	Calendars        int  `json:"calendars"`        // calendar visibility/color choices restored
	SecretsRestored  bool `json:"secretsRestored"`  // API keys / OAuth app credentials were in the file and restored
}
