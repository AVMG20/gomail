// Package syncserver is gomail's device-sync server: a small JSON API that
// keeps a few encrypted documents per account, each with a revision
// number, so several gomail installs can share settings, labels and
// sign-ins. An account is named by an id the device derives from its sync
// code; devices encrypt everything before upload (see internal/devsync), so
// the server only stores ciphertext and a salted hash of each account's
// auth key. It needs no cgo (pure-Go SQLite).
package syncserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/syncapi"
)

// Config configures a Server.
type Config struct {
	DataDir string // holds sync.db
	// Signups allows new accounts (existing ones keep working either way).
	Signups bool
	// MaxDocBytes caps one document, QuotaBytes an account's documents
	// together, MaxDocs their number. 0 = defaults.
	MaxDocBytes int
	QuotaBytes  int64
	MaxDocs     int
	// TrustProxy takes the client address from X-Forwarded-For (the last
	// entry, added by the reverse proxy) for rate limiting. Only enable it
	// behind a proxy that sets the header.
	TrustProxy bool
	// SessionTTL is how long a login lasts (default 30 days).
	SessionTTL time.Duration
}

// Defaults for Config.
const (
	DefaultMaxDocBytes = 16 << 20
	DefaultQuotaBytes  = 64 << 20
	DefaultMaxDocs     = 64
	DefaultSessionTTL  = 30 * 24 * time.Hour
	authKeySize        = 32
)

// Server serves the sync API.
type Server struct {
	cfg       Config
	db        *db
	mux       *http.ServeMux
	byIP      *limiter // sign-ups and logins per client address
	byAccount *limiter // logins per account
}

// New opens (creating) the database in cfg.DataDir.
func New(cfg Config) (*Server, error) {
	if cfg.MaxDocBytes <= 0 {
		cfg.MaxDocBytes = DefaultMaxDocBytes
	}
	if cfg.QuotaBytes <= 0 {
		cfg.QuotaBytes = DefaultQuotaBytes
	}
	if cfg.MaxDocs <= 0 {
		cfg.MaxDocs = DefaultMaxDocs
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = DefaultSessionTTL
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	d, err := openDB(filepath.Join(cfg.DataDir, "sync.db"))
	if err != nil {
		return nil, fmt.Errorf("opening the database: %w", err)
	}
	s := &Server{cfg: cfg, db: d, mux: http.NewServeMux(),
		byIP: newLimiter(30, 10*time.Minute), byAccount: newLimiter(10, 15*time.Minute)}
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("POST /v1/register", s.register)
	s.mux.HandleFunc("POST /v1/login", s.login)
	s.mux.HandleFunc("GET /v1/docs", s.auth(s.listDocs))
	s.mux.HandleFunc("GET /v1/docs/{name}", s.auth(s.getDoc))
	s.mux.HandleFunc("PUT /v1/docs/{name}", s.auth(s.putDoc))
	return s, nil
}

// Close closes the database.
func (s *Server) Close() error { return s.db.close() }

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.ping(); err != nil {
		writeError(w, http.StatusServiceUnavailable, syncapi.ErrInternal, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- accounts ----

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, s.byIP, "ip:"+s.clientIP(r)) {
		return
	}
	var req syncapi.RegisterRequest
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	account, key, ok := credentials(w, req.Account, req.AuthKey)
	if !ok {
		return
	}
	if !s.cfg.Signups {
		writeError(w, http.StatusForbidden, syncapi.ErrSignupsClosed, "this server doesn't take new sync accounts")
		return
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	created, err := s.db.createAccount(account, salt, key)
	if err != nil {
		s.internal(w, "register", err)
		return
	}
	if !created {
		writeError(w, http.StatusConflict, syncapi.ErrAccountExists, "this account exists already")
		return
	}
	slog.Info("account created")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, s.byIP, "ip:"+s.clientIP(r)) {
		return
	}
	var req syncapi.LoginRequest
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	account, key, ok := credentials(w, req.Account, req.AuthKey)
	if !ok || !s.limit(w, s.byAccount, account) {
		return
	}
	good, err := s.db.checkAccount(account, key)
	if err != nil {
		s.internal(w, "login", err)
		return
	}
	if !good {
		writeError(w, http.StatusUnauthorized, syncapi.ErrBadCredentials, "no sync account for this code")
		return
	}
	s.byAccount.reset(account)
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	exp := time.Now().Add(s.cfg.SessionTTL).UTC().Truncate(time.Second)
	if err := s.db.addSession(tokenHash(token), account, exp); err != nil {
		s.internal(w, "login", err)
		return
	}
	writeJSON(w, http.StatusOK, syncapi.LoginResponse{Token: token, ExpiresAt: exp})
}

// credentials validates an account id and base64 auth key.
func credentials(w http.ResponseWriter, account, authKey string) (string, []byte, bool) {
	if !syncapi.ValidAccount(account) {
		writeError(w, http.StatusBadRequest, syncapi.ErrBadRequest, "malformed account id")
		return "", nil, false
	}
	key, err := base64.StdEncoding.DecodeString(authKey)
	if err != nil || len(key) != authKeySize {
		writeError(w, http.StatusBadRequest, syncapi.ErrBadRequest, "malformed auth key")
		return "", nil, false
	}
	return account, key, true
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// auth wraps a handler that needs a session; it gets the account id.
func (s *Server) auth(next func(w http.ResponseWriter, r *http.Request, account string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" || len(token) > 256 {
			writeError(w, http.StatusUnauthorized, syncapi.ErrUnauthorized, "sign in first")
			return
		}
		account, err := s.db.session(tokenHash(token))
		if errors.Is(err, errNotFound) {
			writeError(w, http.StatusUnauthorized, syncapi.ErrUnauthorized, "the session expired")
			return
		}
		if err != nil {
			s.internal(w, "session", err)
			return
		}
		next(w, r, account)
	}
}

// ---- documents ----

func (s *Server) listDocs(w http.ResponseWriter, _ *http.Request, account string) {
	rows, err := s.db.listDocs(account)
	if err != nil {
		s.internal(w, "list", err)
		return
	}
	out := syncapi.DocList{Docs: []syncapi.DocInfo{}, QuotaBytes: s.cfg.QuotaBytes}
	for _, d := range rows {
		out.Docs = append(out.Docs, syncapi.DocInfo{Name: d.name, Rev: d.rev, Size: d.size, UpdatedAt: d.updated})
		out.UsedBytes += int64(d.size)
	}
	writeJSON(w, http.StatusOK, out)
}

func docName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if !syncapi.ValidDocName(name) {
		writeError(w, http.StatusBadRequest, syncapi.ErrBadRequest, "invalid document name")
		return "", false
	}
	return name, true
}

func (s *Server) getDoc(w http.ResponseWriter, r *http.Request, account string) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	rev, data, err := s.db.getDoc(account, name)
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, syncapi.ErrNotFound, "no such document")
		return
	}
	if err != nil {
		s.internal(w, "get", err)
		return
	}
	writeJSON(w, http.StatusOK, syncapi.Doc{Name: name, Rev: rev, Data: data})
}

func (s *Server) putDoc(w http.ResponseWriter, r *http.Request, account string) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	var req syncapi.PutRequest
	// Base64 makes the body a third larger than the document.
	if !readJSON(w, r, int64(s.cfg.MaxDocBytes)*4/3+4<<10, &req) {
		return
	}
	if len(req.Data) == 0 || req.BaseRev < 0 {
		writeError(w, http.StatusBadRequest, syncapi.ErrBadRequest, "empty document")
		return
	}
	if len(req.Data) > s.cfg.MaxDocBytes {
		writeError(w, http.StatusRequestEntityTooLarge, syncapi.ErrTooLarge, "the document is too large")
		return
	}
	res, err := s.db.putDoc(account, name, req.BaseRev, req.Data, s.cfg.QuotaBytes, s.cfg.MaxDocs)
	if err != nil {
		s.internal(w, "put", err)
		return
	}
	switch {
	case res.conflict:
		writeJSON(w, http.StatusConflict, syncapi.Error{Code: syncapi.ErrConflict, Message: "the document changed", Rev: res.rev})
	case res.quota:
		writeError(w, http.StatusInsufficientStorage, syncapi.ErrQuota, "the account's storage on this server is full")
	default:
		writeJSON(w, http.StatusOK, syncapi.PutResponse{Rev: res.rev})
	}
}

// ---- helpers ----

func (s *Server) limit(w http.ResponseWriter, l *limiter, key string) bool {
	ok, wait := l.allow(key)
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, syncapi.ErrRateLimited, "too many attempts; try again later")
	}
	return ok
}

// clientIP is the address rate limits count against.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) internal(w http.ResponseWriter, op string, err error) {
	slog.Error("request failed", "op", op, "err", err)
	writeError(w, http.StatusInternalServerError, syncapi.ErrInternal, "something went wrong on the server")
}

func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, syncapi.ErrTooLarge, "the request is too large")
		} else {
			writeError(w, http.StatusBadRequest, syncapi.ErrBadRequest, "malformed request")
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, syncapi.Error{Code: code, Message: msg})
}
