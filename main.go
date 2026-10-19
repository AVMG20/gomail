package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/AVMG20/gomail/internal/appicon"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/models"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Typed events: the binding generator emits TypeScript types for these.
	application.RegisterEvent[models.SyncEvent](models.EventSyncStarted)
	application.RegisterEvent[models.SyncEvent](models.EventSyncFinished)
	application.RegisterEvent[models.AccountEvent](models.EventMessagesChanged)
	application.RegisterEvent[models.AccountEvent](models.EventAccountsChanged)
	application.RegisterEvent[models.AccountEvent](models.EventLabelsChanged)
	application.RegisterEvent[models.ClassifyProgress](models.EventClassifyProgress)
	application.RegisterEvent[models.ClassifyProgress](models.EventClassifyFinished)
	application.RegisterEvent[models.NewMailEvent](models.EventNewMail)
	application.RegisterEvent[models.SyncProgress](models.EventSyncProgress)
	application.RegisterEvent[models.NetworkStatus](models.EventNetworkStatus)
	application.RegisterEvent[models.SearchUpdated](models.EventSearchUpdated)
	application.RegisterEvent[models.AIBudgetEvent](models.EventAIBudget)
	application.RegisterEvent[models.NotificationOpen](models.EventNotificationOpen)
	application.RegisterEvent[models.DatabaseStatus](models.EventDatabaseStatus)
	application.RegisterEvent[models.ComposeOpen](models.EventComposeOpen)
	application.RegisterEvent[models.UpdateInfo](models.EventUpdateAvailable)
}

// webviewStorage keeps the webview's localStorage/cache inside the data
// directory so separate profiles (GOMAIL_DATA_DIR, demo) don't share it.
// Windows: WebView2 user data folder. Linux: WebKitGTK uses XDG_DATA_HOME /
// XDG_CACHE_HOME, redirected only for custom profiles to avoid side effects
// on the default one. macOS: WKWebView storage is per bundle id (no option).
func webviewStorage() string {
	dir, err := config.DataDir()
	if err != nil {
		return ""
	}
	wv := filepath.Join(dir, "webview")
	if runtime.GOOS == "linux" && config.CustomProfile() {
		_ = os.Setenv("XDG_DATA_HOME", filepath.Join(wv, "data"))
		_ = os.Setenv("XDG_CACHE_HOME", filepath.Join(wv, "cache"))
	}
	return wv
}

// minimizedArg is passed when launched at login: start hidden in the tray.
const minimizedArg = "--minimized"

// instanceID identifies the single running instance per data directory
// (a second launch focuses it instead of opening the database twice).
func instanceID() string {
	id := "com.avmg20.gomail"
	if config.CustomProfile() {
		if dir, err := config.DataDir(); err == nil {
			sum := sha256.Sum256([]byte(dir))
			id += "." + hex.EncodeToString(sum[:6])
		}
	}
	return id
}

func main() {
	webviewDir := webviewStorage()
	notifier := notifications.New()
	svc := NewMailService(notifier)
	args := os.Args[1:]
	svc.startMinimized = slices.Contains(args, minimizedArg)
	svc.handleArgs(args)

	app := application.New(application.Options{
		Name:        "gomail",
		Description: "A fast desktop mail client for Gmail and Outlook with AI labels",
		// The notification service is started by MailService so a missing
		// notification daemon (minimal Linux desktops) can't stop the app.
		Services: []application.Service{application.NewService(svc)},
		Assets:   application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Windows:  application.WindowsOptions{WebviewUserDataPath: webviewDir},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: instanceID(),
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				svc.showWindow()
				svc.handleArgs(d.Args)
			},
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "gomail",
		Width:            1280,
		Height:           820,
		MinWidth:         900,
		MinHeight:        600,
		URL:              "/",
		BackgroundColour: application.NewRGB(27, 38, 54),
		Hidden:           svc.startMinimized, // shown again unless the tray is available
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})
	svc.attach(app, window)

	// System tray: unread count + quick actions.
	tray := app.SystemTray.New()
	tray.SetIcon(appicon.PNG)
	tray.SetTooltip("gomail")
	menu := application.NewMenu()
	menu.Add("Open gomail").OnClick(func(*application.Context) { svc.showWindow() })
	menu.Add("Check mail").OnClick(func(*application.Context) { go svc.SyncNow("") })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	tray.OnClick(func() { svc.showWindow() })
	svc.tray = tray
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { svc.applyTray() })
	// macOS delivers mailto: links as an "open URL" event, not as arguments.
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		svc.showWindow()
		svc.handleArgs([]string{e.Context().URL()})
	})

	// Close-to-tray: hide instead of closing when enabled.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if svc.hideOnClose() {
			e.Cancel()
			window.Hide()
		}
	})

	notifier.OnNotificationResponse(svc.onNotification)

	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gomail:", err)
		os.Exit(1)
	}
}
