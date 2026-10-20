// Package winstate remembers the main window's geometry between runs and
// decides, on launch, whether the saved geometry still makes sense on the
// screens that are connected now. A broken or stale file never sticks: any
// value that can't be placed sensibly falls back to the defaults.
package winstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the state file inside the data directory.
const FileName = "window.json"

// Sanity limits for saved values: anything outside is treated as garbage.
const (
	maxDimension = 16384   // px; larger than any realistic window
	maxCoord     = 100_000 // px; virtual desktops are never this wide
)

// Rect is a rectangle in the window system's logical (DIP) coordinates.
type Rect struct {
	X, Y, Width, Height int
}

// Screen is a connected display.
type Screen struct {
	Bounds   Rect
	WorkArea Rect // Bounds minus the menu bar, dock and taskbars
	Primary  bool
}

// State is the saved window geometry. X/Y/Width/Height are the normal
// (un-maximised) bounds; Maximised says whether to maximise on top of them.
type State struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

// Rect returns the normal bounds.
func (s State) Rect() Rect { return Rect{s.X, s.Y, s.Width, s.Height} }

// Valid reports whether the values are plausible at all (not zero,
// negative or absurd). It says nothing about the connected screens.
func (s State) Valid() bool {
	return s.Width > 0 && s.Height > 0 && s.Width <= maxDimension && s.Height <= maxDimension &&
		abs(s.X) <= maxCoord && abs(s.Y) <= maxCoord
}

// Load reads the saved state. It fails on a missing, corrupt or implausible
// file; callers then use the default geometry.
func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("corrupt window state: %w", err)
	}
	if !s.Valid() {
		return State{}, errors.New("implausible window state")
	}
	return s, nil
}

// Save writes the state atomically (a crash mid-write can't leave a
// half-written file behind).
func Save(path string, s State) error {
	if !s.Valid() {
		return errors.New("refusing to save an implausible window state")
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".window-*.json")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Restore turns the saved state into a placement for the connected screens.
// ok is false when the saved state is unusable (or there are no screens):
// the caller then keeps its default size and position.
//
//   - The window goes back where it was when at least half of it (or of the
//     screen, for windows bigger than the screen) is on one screen's work
//     area. Otherwise (e.g. that monitor was unplugged) it is centered on
//     the primary screen.
//   - The size is clamped to at least minW x minH and at most the chosen
//     screen's work area (the minimum wins on screens smaller than it).
//   - The position is then shifted so the window lies fully inside the
//     work area, so the title bar is always reachable.
//   - Maximised is carried over.
func Restore(saved State, screens []Screen, minW, minH int) (State, bool) {
	if !saved.Valid() || len(screens) == 0 {
		return State{}, false
	}
	r := saved.Rect()

	var target *Screen
	bestArea := 0
	for i := range screens {
		wa := screens[i].workArea()
		if a := overlapArea(r, wa); a > bestArea {
			target, bestArea = &screens[i], a
		}
	}
	center := false
	if target == nil || bestArea*2 < min(area(r), area(target.workArea())) {
		target, center = primary(screens), true
	}
	wa := target.workArea()
	if wa.Width <= 0 || wa.Height <= 0 {
		return State{}, false
	}

	out := State{Maximised: saved.Maximised}
	out.Width = clamp(r.Width, minW, wa.Width)
	out.Height = clamp(r.Height, minH, wa.Height)
	if center {
		out.X = wa.X + (wa.Width-out.Width)/2
		out.Y = wa.Y + (wa.Height-out.Height)/2
	} else {
		out.X = clamp(r.X, wa.X, wa.X+wa.Width-out.Width)
		out.Y = clamp(r.Y, wa.Y, wa.Y+wa.Height-out.Height)
	}
	// A window larger than its screen (the minimum size wins) starts at the
	// work area's top-left corner.
	out.X, out.Y = max(out.X, wa.X), max(out.Y, wa.Y)
	return out, true
}

func (s *Screen) workArea() Rect {
	if s.WorkArea.Width > 0 && s.WorkArea.Height > 0 {
		return s.WorkArea
	}
	return s.Bounds
}

func primary(screens []Screen) *Screen {
	for i := range screens {
		if screens[i].Primary {
			return &screens[i]
		}
	}
	return &screens[0]
}

// clamp limits v to [lo, hi]; lo wins when hi < lo.
func clamp(v, lo, hi int) int {
	return max(min(v, hi), lo)
}

func overlapArea(a, b Rect) int {
	w := min(a.X+a.Width, b.X+b.Width) - max(a.X, b.X)
	h := min(a.Y+a.Height, b.Y+b.Height) - max(a.Y, b.Y)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

func area(r Rect) int { return r.Width * r.Height }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
