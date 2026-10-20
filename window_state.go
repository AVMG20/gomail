package main

import (
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/AVMG20/gomail/internal/safe"
	"github.com/AVMG20/gomail/internal/winstate"
)

const (
	// windowSaveDelay debounces saves while the window is dragged/resized.
	windowSaveDelay = 500 * time.Millisecond
	// screensWait bounds how long the restore waits for the screen list
	// (Wails fills it on launch, concurrently with ApplicationStarted).
	screensWait = 2 * time.Second
)

// windowKeeper saves the main window's geometry to window.json in the data
// directory and restores it on launch. It never blocks startup: any error
// leaves the default size and position.
type windowKeeper struct {
	path       string
	app        *application.App
	window     *application.WebviewWindow
	minW, minH int
	hidden     bool // started hidden in the tray: maximise only once shown

	mu     sync.Mutex
	ready  bool           // restore finished; events before it are the defaults settling
	timer  *time.Timer    // pending debounced save
	saved  winstate.State // last state written (or loaded)
	normal winstate.Rect  // last un-maximised bounds
	// pendingMax: restored maximised but still hidden in the tray; saves
	// keep the flag until the window is shown and maximised.
	pendingMax bool
}

func newWindowKeeper(dataDir string, app *application.App, w *application.WebviewWindow, minW, minH int, hidden bool) *windowKeeper {
	k := &windowKeeper{path: filepath.Join(dataDir, winstate.FileName), app: app, window: w, minW: minW, minH: minH, hidden: hidden}
	for _, ev := range []events.WindowEventType{
		events.Common.WindowDidResize, events.Common.WindowDidMove,
		events.Common.WindowMaximise, events.Common.WindowUnMaximise,
	} {
		w.OnWindowEvent(ev, func(*application.WindowEvent) { k.schedule() })
	}
	// Save on quit while the window still exists (tray Quit, Cmd+Q, ...).
	app.OnShutdown(k.flush)
	return k
}

// restore applies the saved geometry if it still fits the connected
// screens. Runs once the application has started (screens are known).
func (k *windowKeeper) restore() {
	defer safe.Recover("window restore")
	defer func() {
		k.mu.Lock()
		k.ready = true
		k.mu.Unlock()
	}()
	saved, err := winstate.Load(k.path)
	if err != nil {
		slog.Debug("no usable saved window geometry; using the defaults", "err", err)
		return
	}
	k.mu.Lock()
	k.saved = saved
	k.mu.Unlock()
	place, ok := winstate.Restore(saved, k.screens(), k.minW, k.minH)
	if !ok {
		slog.Info("saved window geometry doesn't fit the connected screens; using the defaults")
		return
	}
	k.mu.Lock()
	k.normal = place.Rect()
	k.pendingMax = place.Maximised && k.hidden
	k.mu.Unlock()
	// Size first: on macOS a resize keeps the bottom edge, the position
	// then puts the top-left corner in place.
	k.window.SetSize(place.Width, place.Height)
	k.window.SetPosition(place.X, place.Y)
	switch {
	case !place.Maximised:
	case !k.hidden:
		k.window.Maximise()
	default:
		// Maximising shows the window on some platforms (Windows): wait
		// until the user opens it from the tray.
		var once sync.Once
		k.window.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
			once.Do(func() {
				defer safe.Recover("window maximise")
				k.window.Maximise()
				k.mu.Lock()
				k.pendingMax = false
				k.mu.Unlock()
			})
		})
	}
}

// screens waits briefly until the native window exists and Wails has
// listed the screens (both happen concurrently with ApplicationStarted).
func (k *windowKeeper) screens() []winstate.Screen {
	deadline := time.Now().Add(screensWait)
	var all []*application.Screen
	for {
		all = k.app.Screen.GetAll()
		if (len(all) > 0 && k.window.Bounds().Width > 0) || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	out := make([]winstate.Screen, 0, len(all))
	for _, s := range all {
		if s == nil {
			continue
		}
		out = append(out, winstate.Screen{
			Bounds:   winstate.Rect(s.Bounds),
			WorkArea: winstate.Rect(s.WorkArea),
			Primary:  s.IsPrimary,
		})
	}
	return out
}

// schedule debounces a save after a move/resize/maximise.
func (k *windowKeeper) schedule() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.ready {
		return
	}
	if k.timer != nil {
		k.timer.Stop()
	}
	k.timer = time.AfterFunc(windowSaveDelay, func() {
		defer safe.Recover("window save")
		k.mu.Lock()
		k.timer = nil
		k.mu.Unlock()
		k.save()
	})
}

// flush saves right away (window closing, app quitting).
func (k *windowKeeper) flush() {
	defer safe.Recover("window save")
	k.mu.Lock()
	if k.timer != nil {
		k.timer.Stop()
		k.timer = nil
	}
	ready := k.ready
	k.mu.Unlock()
	if ready {
		k.save()
	}
}

// save captures the window's geometry and writes it when it changed.
// Minimised and fullscreen windows are skipped: their bounds aren't what
// the user wants back.
func (k *windowKeeper) save() {
	w := k.window
	if w.IsMinimised() || w.IsFullscreen() {
		return
	}
	maximised := w.IsMaximised()
	b := w.Bounds()
	if b.Width <= 0 || b.Height <= 0 {
		return // a destroyed window reports 0x0
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	maximised = maximised || k.pendingMax
	if !maximised || k.normal.Width == 0 {
		k.normal = winstate.Rect(b)
	}
	st := winstate.State{X: k.normal.X, Y: k.normal.Y, Width: k.normal.Width, Height: k.normal.Height, Maximised: maximised}
	if !st.Valid() || st == k.saved {
		return
	}
	if err := winstate.Save(k.path, st); err != nil {
		slog.Warn("cannot save the window geometry", "err", err)
		return
	}
	k.saved = st
}
