package main

import "github.com/godbus/dbus/v5"

// trayAvailable reports whether a StatusNotifier host (tray) is running.
// Without one (e.g. GNOME without the AppIndicator extension), hiding the
// window to the tray would leave it unreachable.
func trayAvailable() bool {
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return false
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return false
	}
	if err := conn.Hello(); err != nil {
		return false
	}
	var owner string
	err = conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, "org.kde.StatusNotifierWatcher").Store(&owner)
	return err == nil && owner != ""
}

// sessionBusAvailable reports whether a D-Bus session bus can be reached
// without auto-launching one. Single-instance mode needs it on Linux; without
// it (minimal window managers, containers) gomail still starts, just without
// focusing an already running instance.
func sessionBusAvailable() bool {
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return false
	}
	defer conn.Close()
	return conn.Auth(nil) == nil && conn.Hello() == nil
}
