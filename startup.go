package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/instance"
)

// errAlreadyRunning is the init error of a process that found the data
// directory locked by another gomail.
var errAlreadyRunning = errors.New("gomail is already running with this data directory")

// startup holds what main prepares before Wails starts.
type startup struct {
	dir       string
	logFile   string
	logCloser io.Closer
}

// prepareStartup creates the data directory and the log file. Failures are
// fatal and reported on stderr (there is nowhere else yet).
func prepareStartup() (startup, error) {
	dir, err := config.DataDir()
	if err != nil {
		return startup{}, err
	}
	st := startup{dir: dir}
	logFile, closer, err := config.SetupLogging(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gomail: logging to a file is unavailable:", err)
	} else {
		st.logFile, st.logCloser = logFile, closer
	}
	return st, nil
}

// fatalStartup reports a startup failure on stderr and in the log file
// (slog writes to both once logging is set up) and exits.
func fatalStartup(msg string, err error) {
	slog.Error(msg, "err", err)
	fmt.Fprintf(os.Stderr, "gomail: %s: %v\n", msg, err)
	os.Exit(1)
}

// singleInstanceOptions enables Wails' single-instance mode when the
// platform mechanism works. Otherwise it returns nil: gomail then runs as a
// normal instance and the data-directory lock (claimInstance) keeps a
// second process away from the database.
func singleInstanceOptions(usable bool, id string, onSecond func(application.SecondInstanceData)) *application.SingleInstanceOptions {
	if !usable {
		slog.Warn("single-instance mode is unavailable (no D-Bus session bus or the name can't be owned); continuing without it")
		return nil
	}
	return &application.SingleInstanceOptions{UniqueID: id, OnSecondInstanceLaunch: onSecond}
}

// claimInstance takes the data-directory lock. running reports that
// another gomail holds it. Other failures (read-only directory, exotic file
// systems) only disable the guard; they never stop gomail from starting.
func claimInstance(dir string) (lock *instance.Lock, running bool) {
	l, err := instance.Acquire(dir)
	switch {
	case err == nil:
		return l, false
	case errors.Is(err, instance.ErrLocked):
		return nil, true
	default:
		slog.Warn("instance lock unavailable; continuing without it", "err", err)
		return nil, false
	}
}

// alreadyRunningMessage is shown when the lock is held.
const alreadyRunningMessage = "gomail is already running. Switch to the open gomail window (or its tray icon) instead of starting it again."

// showAlreadyRunning tells the user (stderr, log, native dialog) and quits.
// No window, tray or database is opened in this process.
func showAlreadyRunning(app *application.App) {
	slog.Error("another gomail process holds the data directory lock; exiting", "lock", instance.FileName)
	fmt.Fprintln(os.Stderr, "gomail:", alreadyRunningMessage)
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// Quit even if the dialog can't be shown or is never answered.
		time.AfterFunc(time.Minute, app.Quit)
		go func() {
			d := app.Dialog.Error().SetTitle("gomail").SetMessage(alreadyRunningMessage)
			ok := d.AddButton("OK")
			ok.OnClick(app.Quit)
			d.SetDefaultButton(ok)
			d.Show()
		}()
	})
}

// logWailsError routes Wails errors to the log; fatal ones (Wails exits
// right after) also get a clear line on stderr.
func logWailsError(err error) {
	var fe *application.FatalError
	if errors.As(err, &fe) {
		slog.Error("fatal error, gomail is exiting", "err", err)
		fmt.Fprintln(os.Stderr, "gomail: fatal error:", err)
		return
	}
	slog.Error("wails error", "err", err)
}
