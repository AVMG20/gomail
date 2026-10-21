// Package syncapi is the wire format between gomail and its device-sync
// server (cmd/gomail-sync). The server stores opaque, client-encrypted
// documents with a revision number; it never sees keys or plaintext.
//
// Endpoints (JSON over HTTPS):
//
//	GET  /healthz                 liveness
//	POST /v1/register             RegisterRequest -> 201
//	POST /v1/login                LoginRequest -> LoginResponse
//	GET  /v1/docs                 (Bearer) -> DocList
//	GET  /v1/docs/{name}          (Bearer) -> Doc
//	PUT  /v1/docs/{name}          (Bearer) PutRequest -> PutResponse, 409 ErrConflict
//
// Errors are an Error body with a stable Code.
package syncapi

import (
	"regexp"
	"strings"
	"time"
)

// Error codes.
const (
	ErrBadRequest       = "bad_request"
	ErrUnauthorized     = "unauthorized"    // missing/expired session
	ErrBadCredentials   = "bad_credentials" // wrong username or passphrase
	ErrUserExists       = "user_exists"     // register: the name is taken
	ErrSignupsClosed    = "signups_closed"  // register: the server takes no new accounts
	ErrSignupCode       = "signup_code"     // register: a sign-up code is needed (or was wrong)
	ErrRateLimited      = "rate_limited"    // too many attempts; see Retry-After
	ErrNotFound         = "not_found"       // no such document
	ErrConflict         = "conflict"        // PUT: BaseRev isn't the current revision
	ErrTooLarge         = "too_large"       // document over the size limit
	ErrQuota            = "quota"           // account storage or document count full
	ErrInternal         = "internal"        // server-side failure
	ErrMethodNotAllowed = "method_not_allowed"
)

// Error is the body of every non-2xx response.
type Error struct {
	Code    string `json:"error"`
	Message string `json:"message"`
	Rev     int64  `json:"rev,omitempty"` // ErrConflict: the current revision
}

// RegisterRequest creates an account. AuthKey is the base64 (std) of the
// 32-byte key derived on the device; the server keeps only a salted hash.
type RegisterRequest struct {
	Username   string `json:"username"`
	AuthKey    string `json:"authKey"`
	SignupCode string `json:"signupCode,omitempty"`
}

// LoginRequest exchanges the auth key for a session token.
type LoginRequest struct {
	Username string `json:"username"`
	AuthKey  string `json:"authKey"`
}

// LoginResponse holds a bearer token for the other endpoints.
type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// DocInfo describes a stored document.
type DocInfo struct {
	Name      string    `json:"name"`
	Rev       int64     `json:"rev"`
	Size      int       `json:"size"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DocList lists an account's documents and its storage use.
type DocList struct {
	Docs       []DocInfo `json:"docs"`
	UsedBytes  int64     `json:"usedBytes"`
	QuotaBytes int64     `json:"quotaBytes"`
}

// Doc is a stored document. Data is ciphertext (base64 std in JSON).
type Doc struct {
	Name string `json:"name"`
	Rev  int64  `json:"rev"`
	Data []byte `json:"data"`
}

// PutRequest replaces a document if its revision is still BaseRev (0 for
// a document that doesn't exist yet).
type PutRequest struct {
	BaseRev int64  `json:"baseRev"`
	Data    []byte `json:"data"`
}

// PutResponse is the document's new revision.
type PutResponse struct {
	Rev int64 `json:"rev"`
}

var (
	usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._@+-]{2,63}$`)
	docNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

// NormalizeUsername lower-cases and trims a username.
func NormalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ValidUsername reports whether a (normalized) username is acceptable:
// 3–64 characters of a-z, 0-9 and . _ @ + -.
func ValidUsername(s string) bool { return usernamePattern.MatchString(s) }

// ValidDocName reports whether a document name is acceptable.
func ValidDocName(s string) bool { return docNamePattern.MatchString(s) }
