package winstate

import (
	"os"
	"path/filepath"
	"testing"
)

const minW, minH = 900, 600

// A 1440x900 laptop panel (menu bar on top) as primary, and a 2560x1440
// external monitor to its right.
var (
	laptop = Screen{
		Bounds:   Rect{0, 0, 1440, 900},
		WorkArea: Rect{0, 25, 1440, 875},
		Primary:  true,
	}
	external = Screen{
		Bounds:   Rect{1440, 0, 2560, 1440},
		WorkArea: Rect{1440, 0, 2560, 1400},
	}
)

func TestRestore(t *testing.T) {
	tests := []struct {
		name    string
		saved   State
		screens []Screen
		want    State
		wantOK  bool
	}{
		{
			name:    "happy path keeps geometry",
			saved:   State{X: 100, Y: 80, Width: 1200, Height: 760},
			screens: []Screen{laptop},
			want:    State{X: 100, Y: 80, Width: 1200, Height: 760},
			wantOK:  true,
		},
		{
			name:    "happy path on the external monitor",
			saved:   State{X: 1600, Y: 100, Width: 1800, Height: 1100},
			screens: []Screen{laptop, external},
			want:    State{X: 1600, Y: 100, Width: 1800, Height: 1100},
			wantOK:  true,
		},
		{
			name:    "maximised is carried over",
			saved:   State{X: 100, Y: 80, Width: 1200, Height: 760, Maximised: true},
			screens: []Screen{laptop},
			want:    State{X: 100, Y: 80, Width: 1200, Height: 760, Maximised: true},
			wantOK:  true,
		},
		{
			name:    "unplugged monitor centers on primary",
			saved:   State{X: 1600, Y: 100, Width: 1800, Height: 1100, Maximised: true},
			screens: []Screen{laptop},
			// clamped to the laptop's work area, then centered in it
			want:   State{X: 0, Y: 25, Width: 1440, Height: 875, Maximised: true},
			wantOK: true,
		},
		{
			name:    "unplugged monitor, small window is centered",
			saved:   State{X: 2000, Y: 300, Width: 1000, Height: 700},
			screens: []Screen{laptop},
			want:    State{X: 220, Y: 112, Width: 1000, Height: 700},
			wantOK:  true,
		},
		{
			name:    "too big is clamped to the work area",
			saved:   State{X: 0, Y: 0, Width: 5000, Height: 4000},
			screens: []Screen{laptop},
			want:    State{X: 0, Y: 25, Width: 1440, Height: 875},
			wantOK:  true,
		},
		{
			name:    "too small is raised to the minimum",
			saved:   State{X: 50, Y: 60, Width: 300, Height: 200},
			screens: []Screen{laptop},
			want:    State{X: 50, Y: 60, Width: minW, Height: minH},
			wantOK:  true,
		},
		{
			name:    "screen smaller than the minimum: minimum wins at top-left",
			saved:   State{X: 0, Y: 0, Width: 1000, Height: 700},
			screens: []Screen{{Bounds: Rect{0, 0, 800, 600}, WorkArea: Rect{0, 0, 800, 560}, Primary: true}},
			want:    State{X: 0, Y: 0, Width: minW, Height: minH},
			wantOK:  true,
		},
		{
			name:    "slightly negative position is pulled back on screen",
			saved:   State{X: -300, Y: -10, Width: 1200, Height: 760},
			screens: []Screen{laptop},
			want:    State{X: 0, Y: 25, Width: 1200, Height: 760},
			wantOK:  true,
		},
		{
			name:    "far negative position falls back to centering",
			saved:   State{X: -5000, Y: -3000, Width: 1200, Height: 760},
			screens: []Screen{laptop},
			want:    State{X: 120, Y: 82, Width: 1200, Height: 760},
			wantOK:  true,
		},
		{
			name:    "hanging off the bottom-right is pulled in",
			saved:   State{X: 1000, Y: 600, Width: 1000, Height: 700},
			screens: []Screen{laptop, external},
			// mostly on the external monitor (its work area ends at y=1400)
			want:   State{X: 1440, Y: 600, Width: 1000, Height: 700},
			wantOK: true,
		},
		{
			name:    "screen above the primary (negative Y) is kept",
			saved:   State{X: 100, Y: -1000, Width: 1200, Height: 800},
			screens: []Screen{laptop, {Bounds: Rect{0, -1080, 1920, 1080}, WorkArea: Rect{0, -1080, 1920, 1080}}},
			want:    State{X: 100, Y: -1000, Width: 1200, Height: 800},
			wantOK:  true,
		},
		{
			name:    "no primary flag uses the first screen",
			saved:   State{X: 9000, Y: 9000, Width: 1000, Height: 700},
			screens: []Screen{{Bounds: Rect{0, 0, 1920, 1080}}},
			want:    State{X: 460, Y: 190, Width: 1000, Height: 700},
			wantOK:  true,
		},
		{name: "zero size is ignored", saved: State{X: 10, Y: 10}, screens: []Screen{laptop}},
		{name: "negative size is ignored", saved: State{Width: -1280, Height: 820}, screens: []Screen{laptop}},
		{name: "absurd size is ignored", saved: State{Width: 1 << 20, Height: 820}, screens: []Screen{laptop}},
		{name: "absurd position is ignored", saved: State{X: -1 << 30, Width: 1280, Height: 820}, screens: []Screen{laptop}},
		{name: "no screens", saved: State{Width: 1280, Height: 820}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Restore(tt.saved, tt.screens, minW, minH)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("Restore = %+v, %v; want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		content string // "" = no file
		want    State
		wantErr bool
	}{
		{name: "valid", content: `{"x":10,"y":20,"width":1280,"height":820,"maximised":true}`,
			want: State{X: 10, Y: 20, Width: 1280, Height: 820, Maximised: true}},
		{name: "missing file", wantErr: true},
		{name: "corrupt json", content: `{"x":10,"wid`, wantErr: true},
		{name: "binary garbage", content: "\x00\xff\x13", wantErr: true},
		{name: "wrong types", content: `{"width":"big","height":820}`, wantErr: true},
		{name: "zero size", content: `{"x":10,"y":20,"width":0,"height":0}`, wantErr: true},
		{name: "empty object", content: `{}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if tt.content != "" {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Load(path)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("Load = %+v, %v; want %+v, err=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	want := State{X: -1500, Y: 40, Width: 1400, Height: 900, Maximised: true}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != want {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
	if err := Save(path, State{Width: 0, Height: 0}); err == nil {
		t.Fatal("saved an implausible state")
	}
	if got, _ := Load(path); got != want {
		t.Fatalf("implausible save overwrote the file: %+v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}
