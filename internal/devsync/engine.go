// Package devsync keeps several gomail installs in sync through a
// gomail-sync server (internal/syncserver). The app describes its state as
// a few documents of keyed JSON values (Source); the engine encrypts them
// with a key derived from the account's sync code and syncs on start, every
// minute, after waking and on request: each round uploads this device's
// changes and takes the other devices', merged per key (last writer wins).
// It isn't real-time; a change reaches the other devices within a minute
// or two.
//
// Each document keeps a local shadow: the entries as last merged. A
// round compares the app's current values with the shadow to find local
// changes, merges them with the server's copy, applies what other devices
// changed, and uploads when the server lacks something. Applying a value
// makes the app's state equal the shadow, so it is never sent back (no
// echo).
package devsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/safe"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/syncapi"
)

// Source is the app state that syncs.
type Source interface {
	// Docs lists the documents this device has, in the order they sync
	// (a document can depend on ones before it).
	Docs() []string
	// Snapshot returns the current values of a document's keys. synced
	// looks up a key's value as last synced (to keep a value that may
	// change locally without it mattering, like a rotated sign-in).
	Snapshot(doc string, synced func(key string) (json.RawMessage, bool)) (map[string]json.RawMessage, error)
	// Owns reports whether a key missing from Snapshot was deleted on
	// this device (true), or is just unknown here (false: e.g. it belongs
	// to a mailbox this device doesn't have, so it is kept).
	Owns(doc, key string) bool
	// Apply sets keys to values another device wrote (nil = deleted). It
	// returns the keys it can't apply yet; they are retried each round
	// and never count as changed here meanwhile.
	Apply(doc string, changes map[string]json.RawMessage) (notApplied []string, err error)
}

// StateStore persists the engine's (non-secret) state.
type StateStore interface {
	// Load decodes key into v; false when it doesn't exist.
	Load(key string, v any) (bool, error)
	Save(key string, v any) error
	// DeletePrefix removes every key starting with prefix.
	DeletePrefix(prefix string) error
}

// Options configure an Engine.
type Options struct {
	Source   Source
	State    StateStore
	Secrets  secrets.Store
	HTTP     *http.Client // nil = default with timeouts
	OnStatus func(Status) // status changes (optional)
	// Interval is how often to sync (read each time; 0 = only on start
	// and SyncNow). nil = every minute.
	Interval func() time.Duration
	Delay    time.Duration    // first round after Start (default 5 s)
	Now      func() time.Time // tests
}

// Status describes the sync connection for the UI.
type Status struct {
	Connected bool
	Server    string
	Secrets   bool // sign-ins and API keys sync
	Syncing   bool
	LastSync  time.Time
	Error     string
}

// State keys (StateStore) and the secret holding the keys.
const (
	StatePrefix  = "devicesync."
	configKey    = StatePrefix + "config"
	docKeyPrefix = StatePrefix + "doc."
	codeSecret   = "devicesync:code"
)

// ErrNotConnected is returned when sync isn't set up.
var ErrNotConnected = errors.New("device sync isn't set up")

type config struct {
	Server   string    `json:"server"`
	Secrets  bool      `json:"secrets"`
	LastSync time.Time `json:"lastSync"`
}

type docState struct {
	Rev     int64    `json:"rev"`               // the server revision merged in last
	Entries Entries  `json:"entries"`           // the shadow
	Pending []string `json:"pending,omitempty"` // keys not applied yet
	Dirty   bool     `json:"dirty,omitempty"`   // the shadow has changes the server lacks
}

// Engine runs device sync.
type Engine struct {
	opts Options

	round sync.Mutex // one round at a time

	mu      sync.Mutex
	cfg     *config // nil = not connected
	keys    Keys
	cl      *client
	syncing bool
	lastErr string
	last    time.Time // last round attempt (wall clock)

	now    chan struct{} // a round right away (Kick)
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates an engine; Start runs it.
func New(opts Options) *Engine {
	if opts.Interval == nil {
		opts.Interval = func() time.Duration { return time.Minute }
	}
	if opts.Delay <= 0 {
		opts.Delay = 5 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	e := &Engine{opts: opts, now: make(chan struct{}, 1)}
	e.load()
	return e
}

// load restores the connection (config and keys) saved by Connect.
func (e *Engine) load() {
	var cfg config
	ok, err := e.opts.State.Load(configKey, &cfg)
	if err != nil || !ok || cfg.Server == "" {
		return
	}
	code, err := e.opts.Secrets.Get(codeSecret)
	if err != nil {
		slog.Warn("device sync: sync code unavailable", "err", err)
		e.lastErr = "This computer's sync code is missing from the keychain. Turn on sync again with the code from another device."
		return
	}
	keys, _, err := KeysFromCode(code)
	if err != nil {
		e.lastErr = "This computer's sync code is damaged. Turn on sync again with the code from another device."
		return
	}
	e.cfg, e.keys = &cfg, keys
	e.cl = newClient(cfg.Server, keys, e.opts.HTTP)
}

// Start runs the background loop until ctx ends or Stop.
func (e *Engine) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancel, e.done = cancel, make(chan struct{})
	e.mu.Unlock()
	go e.loop(ctx)
}

// Stop ends the loop (waiting up to a few seconds for a running round).
func (e *Engine) Stop() {
	e.mu.Lock()
	cancel, done := e.cancel, e.done
	e.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// tickEvery is how often the loop checks whether a pull is due. A wall
// clock jump of several ticks means the computer slept: it syncs then.
const tickEvery = 15 * time.Second

func (e *Engine) loop(ctx context.Context) {
	defer close(e.done)
	defer safe.Recover("device sync")
	first := time.NewTimer(e.opts.Delay)
	defer first.Stop()
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	lastTick := e.opts.Now().Round(0)
	run := func() {
		if e.Connected() {
			_ = e.SyncNow(ctx)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.now:
			run()
		case <-first.C:
			run()
		case <-tick.C:
			// Wall-clock time: the monotonic clock stops while asleep.
			now := e.opts.Now().Round(0)
			e.mu.Lock()
			last := e.last
			e.mu.Unlock()
			if due(now, lastTick, last, e.opts.Interval()) {
				run()
			}
			lastTick = now
		}
	}
}

// due reports whether a periodic round should run at now: the interval
// passed since the last round, or the computer woke from sleep (the wall
// clock jumped since the last tick). interval 0 means only on request.
func due(now, lastTick, lastRound time.Time, interval time.Duration) bool {
	if interval <= 0 {
		return false
	}
	return now.Sub(lastTick) > 4*tickEvery || now.Sub(lastRound) >= interval
}

// Kick syncs now in the background (back online), unless syncing is
// manual.
func (e *Engine) Kick() {
	if !e.Connected() || e.opts.Interval() <= 0 {
		return
	}
	e.runSoon()
}

// runSoon starts a round in the background (after something the user did).
func (e *Engine) runSoon() {
	select {
	case e.now <- struct{}{}:
	default:
	}
}

// Connected reports whether sync is set up.
func (e *Engine) Connected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg != nil
}

// SecretsEnabled reports whether sign-ins and API keys sync.
func (e *Engine) SecretsEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg != nil && e.cfg.Secrets
}

// Status returns the current status.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := Status{Syncing: e.syncing, Error: e.lastErr}
	if e.cfg != nil {
		st.Connected, st.Server, st.Secrets, st.LastSync = true, e.cfg.Server, e.cfg.Secrets, e.cfg.LastSync
	}
	return st
}

func (e *Engine) emit() {
	if e.opts.OnStatus != nil {
		e.opts.OnStatus(e.Status())
	}
}

// ConnectParams start syncing a device.
type ConnectParams struct {
	Server  string
	Code    string // the sync code of another device; "" with Create
	Create  bool   // turn on sync: a new code and sync account
	Secrets bool   // sync sign-ins and API keys (a new account; joining devices follow the account)
}

// Connect turns on sync: with Create, for a new sync account under a new
// code (Code returns it); otherwise with another device's code, taking
// the synced settings and adding what only this device has.
func (e *Engine) Connect(ctx context.Context, p ConnectParams) error {
	server, err := NormalizeServer(p.Server)
	if err != nil {
		return err
	}
	code := p.Code
	if p.Create {
		if code, err = NewCode(); err != nil {
			return err
		}
	}
	keys, code, err := KeysFromCode(code)
	if err != nil {
		return err
	}
	cl := newClient(server, keys, e.opts.HTTP)
	if p.Create {
		if err := cl.register(ctx); err != nil {
			return friendly(err)
		}
	}
	if _, err := cl.login(ctx); err != nil {
		return friendly(err)
	}
	e.round.Lock()
	defer e.round.Unlock()
	if err := e.opts.Secrets.Set(codeSecret, code); err != nil {
		return fmt.Errorf("could not store the sync code securely: %w", err)
	}
	// A fresh start: shadows of an earlier connection don't apply.
	if err := e.opts.State.DeletePrefix(docKeyPrefix); err != nil {
		return err
	}
	cfg := &config{Server: server, Secrets: p.Secrets || !p.Create}
	if err := e.opts.State.Save(configKey, cfg); err != nil {
		return err
	}
	e.mu.Lock()
	e.cfg, e.keys, e.cl, e.lastErr = cfg, keys, cl, ""
	e.mu.Unlock()
	slog.Info("device sync connected", "server", server, "created", p.Create)
	e.emit()
	e.runSoon()
	return nil
}

// Disconnect stops syncing on this device and forgets its keys. Local data
// and the server's copy stay.
func (e *Engine) Disconnect() error {
	e.round.Lock()
	defer e.round.Unlock()
	e.mu.Lock()
	e.cfg, e.keys, e.cl, e.lastErr = nil, Keys{}, nil, ""
	e.mu.Unlock()
	err := errors.Join(e.opts.Secrets.Delete(codeSecret), e.opts.State.DeletePrefix(StatePrefix))
	slog.Info("device sync disconnected")
	e.emit()
	return err
}

// Code returns the sync code of the connected account (to set up another
// device).
func (e *Engine) Code() (string, error) {
	if !e.Connected() {
		return "", ErrNotConnected
	}
	return e.opts.Secrets.Get(codeSecret)
}

// SetSecrets turns syncing of sign-ins and API keys on or off for every
// device of the account.
func (e *Engine) SetSecrets(on bool) error {
	if err := e.setSecrets(on); err != nil {
		return err
	}
	e.runSoon()
	return nil
}

// ApplySecrets records another device's choice (no upload of its own).
func (e *Engine) ApplySecrets(on bool) error { return e.setSecrets(on) }

func (e *Engine) setSecrets(on bool) error {
	e.mu.Lock()
	if e.cfg == nil {
		e.mu.Unlock()
		return ErrNotConnected
	}
	if e.cfg.Secrets == on {
		e.mu.Unlock()
		return nil
	}
	cfg := *e.cfg
	cfg.Secrets = on
	e.cfg = &cfg
	e.mu.Unlock()
	e.emit()
	return e.opts.State.Save(configKey, &cfg)
}

// SyncNow runs a sync round and waits for it.
func (e *Engine) SyncNow(ctx context.Context) error {
	e.round.Lock()
	defer e.round.Unlock()
	e.mu.Lock()
	cl, keys := e.cl, e.keys
	if e.cfg == nil {
		e.mu.Unlock()
		return ErrNotConnected
	}
	e.syncing, e.last = true, e.opts.Now().Round(0)
	e.mu.Unlock()
	e.emit()
	err := e.syncAll(ctx, cl, keys.Enc)
	e.mu.Lock()
	e.syncing = false
	if err != nil {
		e.lastErr = friendly(err).Error()
	} else if e.cfg != nil {
		e.lastErr = ""
		cfg := *e.cfg
		cfg.LastSync = e.opts.Now().UTC().Truncate(time.Second)
		e.cfg = &cfg
		_ = e.opts.State.Save(configKey, &cfg)
	}
	e.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		slog.Warn("device sync failed", "err", err)
	}
	e.emit()
	return err
}

func (e *Engine) syncAll(ctx context.Context, cl *client, encKey []byte) error {
	list, err := cl.list(ctx)
	if err != nil {
		return err
	}
	remote := map[string]int64{}
	for _, d := range list.Docs {
		remote[d.Name] = d.Rev
	}
	var errs []error
	done := map[string]bool{}
	// Two passes: syncing the first documents (accounts) can add documents
	// (a new mailbox's label decisions).
	for range 2 {
		for _, name := range e.opts.Source.Docs() {
			if done[name] {
				continue
			}
			done[name] = true
			if err := e.syncDoc(ctx, cl, encKey, name, remote[name]); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// syncDoc runs one document through a round: local changes into the
// shadow, merge with the server's copy, apply other devices' changes,
// upload when the server lacks something.
func (e *Engine) syncDoc(ctx context.Context, cl *client, encKey []byte, name string, remoteRev int64) error {
	for attempt := 0; ; attempt++ {
		st, err := e.loadDoc(encKey, name)
		if err != nil {
			return err
		}
		local, err := e.opts.Source.Snapshot(name, func(k string) (json.RawMessage, bool) {
			en, ok := st.Entries[k]
			return en.V, ok && !en.D
		})
		if err != nil {
			return err
		}
		now := e.opts.Now()
		mine := localChanges(st, local, func(k string) bool { return e.opts.Source.Owns(name, k) }, now.UnixMilli())

		theirs, rev := st.Entries, st.Rev
		if remoteRev != st.Rev {
			theirs, rev = Entries{}, 0
			if remoteRev > 0 {
				doc, err := cl.get(ctx, name)
				if err != nil {
					return err
				}
				plain, err := Open(encKey, name, doc.Data)
				if err != nil {
					return err
				}
				if theirs, err = decodeDoc(plain); err != nil {
					return err
				}
				rev = doc.Rev
			}
		}
		merged := merge(mine, theirs, now)

		pending := map[string]bool{}
		for _, k := range st.Pending {
			pending[k] = true
		}
		changes := map[string]json.RawMessage{}
		for k, m := range merged {
			if old, ok := mine[k]; pending[k] || !ok || !m.equal(old) {
				if m.D {
					changes[k] = nil
				} else {
					changes[k] = m.V
				}
			}
		}
		var notApplied []string
		if len(changes) > 0 {
			if notApplied, err = e.opts.Source.Apply(name, changes); err != nil {
				return err
			}
		}
		var nextPending []string
		for _, k := range notApplied {
			if _, ok := merged[k]; ok && !slices.Contains(nextPending, k) {
				nextPending = append(nextPending, k)
			}
		}
		slices.Sort(nextPending)
		dirty := st.Dirty || !merged.equal(theirs)
		if dirty != st.Dirty || rev != st.Rev || !slices.Equal(nextPending, st.Pending) || !merged.equal(st.Entries) {
			st.Entries, st.Rev, st.Pending, st.Dirty = merged, rev, nextPending, dirty
			if err := e.saveDoc(encKey, name, st); err != nil {
				return err
			}
		}
		if !st.Dirty {
			return nil
		}
		plain, err := encodeDoc(merged)
		if err != nil {
			return err
		}
		sealed, err := Seal(encKey, name, plain)
		if err != nil {
			return err
		}
		newRev, err := cl.put(ctx, name, rev, sealed)
		var ae *APIError
		if errors.As(err, &ae) && ae.Body.Code == syncapi.ErrConflict && attempt < 4 {
			remoteRev = ae.Body.Rev // another device was faster: merge its version
			continue
		}
		if err != nil {
			return err
		}
		st.Rev, st.Dirty = newRev, false
		return e.saveDoc(encKey, name, st)
	}
}

// localChanges returns the shadow updated with this device's changes: new
// or changed values, and tombstones for owned keys that are gone. A value
// the shadow never had gets timestamp 0, so a value another device synced
// wins over it (a device joining takes the synced settings).
func localChanges(st docState, local map[string]json.RawMessage, owns func(string) bool, now int64) Entries {
	mine := st.Entries.clone()
	pending := map[string]bool{}
	for _, k := range st.Pending {
		pending[k] = true
	}
	for k, v := range local {
		if pending[k] {
			continue
		}
		v = compact(v)
		old, ok := st.Entries[k]
		if ok && !old.D && string(old.V) == string(v) {
			continue
		}
		t := now
		if !ok {
			t = 0
		}
		mine[k] = Entry{V: v, T: t}
	}
	for k, old := range st.Entries {
		if old.D || pending[k] {
			continue
		}
		if _, ok := local[k]; !ok && owns(k) {
			mine[k] = Entry{T: now, D: true}
		}
	}
	return mine
}

// Shadows hold synced sign-ins and API keys, so they are stored encrypted
// with the sync key (kept in the OS keychain).
func localName(name string) string { return "local:" + name }

func (e *Engine) loadDoc(encKey []byte, name string) (docState, error) {
	st := docState{Entries: Entries{}}
	var sealed []byte
	ok, err := e.opts.State.Load(docKeyPrefix+name, &sealed)
	if err != nil || !ok {
		return st, err
	}
	plain, err := Open(encKey, localName(name), sealed)
	if err != nil || json.Unmarshal(plain, &st) != nil {
		// Unreadable (another key): start over; the server has the data.
		slog.Warn("device sync: local state unreadable, starting over", "doc", name)
		return docState{Entries: Entries{}}, nil
	}
	if st.Entries == nil {
		st.Entries = Entries{}
	}
	return st, nil
}

func (e *Engine) saveDoc(encKey []byte, name string, st docState) error {
	plain, err := json.Marshal(st)
	if err != nil {
		return err
	}
	sealed, err := Seal(encKey, localName(name), plain)
	if err != nil {
		return err
	}
	return e.opts.State.Save(docKeyPrefix+name, sealed)
}

// friendly turns server errors into short messages for the UI.
func friendly(err error) error {
	var ae *APIError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Body.Code {
	case syncapi.ErrBadCredentials:
		return errors.New("no sync account uses this code on this server. Check the server address, and that sync is on on the other device")
	case syncapi.ErrAccountExists:
		return errors.New("this sync code is in use already; try again")
	case syncapi.ErrSignupsClosed:
		return errors.New("this server doesn't accept new sync accounts. You can still use a code from a device that syncs with it")
	case syncapi.ErrRateLimited:
		return errors.New("too many attempts. Wait a few minutes and try again")
	case syncapi.ErrQuota:
		return errors.New("your storage on the sync server is full")
	case syncapi.ErrTooLarge:
		return errors.New("there is too much to sync for this server's size limit")
	}
	return err
}
