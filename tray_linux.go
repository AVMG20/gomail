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
