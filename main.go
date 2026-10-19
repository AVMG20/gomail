package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/AVMG20/gomail/internal/appicon"
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
}

func main() {
	notifier := notifications.New()
	svc := NewMailService(notifier)

	app := application.New(application.Options{
		Name:        "gomail",
		Description: "A fast desktop mail client for Gmail and Outlook with AI labels",
		Services: []application.Service{
			application.NewService(svc),
			application.NewService(notifier),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "gomail",
		Width:            1280,
		Height:           820,
		MinWidth:         900,
		MinHeight:        600,
		URL:              "/",
		BackgroundColour: application.NewRGB(27, 38, 54),
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

	notifier.OnNotificationResponse(func(notifications.NotificationResult) { svc.showWindow() })

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
