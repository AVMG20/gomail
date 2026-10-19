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

// singleInstanceUsable reports whether Wails' single-instance mode can
// start: it needs the D-Bus session bus and the right to own
// "<id>.SingleInstance". Wails exits with a fatal error when it can't claim
// the name, so this probes first (without auto-launching a bus). A name
// owned by another process is fine: that is a running gomail to hand off to.
func singleInstanceUsable(id string) bool {
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return false
	}
	defer conn.Close()
	if conn.Auth(nil) != nil || conn.Hello() != nil {
		return false
	}
	name := id + ".SingleInstance"
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return false
	}
	if reply == dbus.RequestNameReplyPrimaryOwner {
		_, _ = conn.ReleaseName(name)
	}
	return reply == dbus.RequestNameReplyPrimaryOwner || reply == dbus.RequestNameReplyExists || reply == dbus.RequestNameReplyAlreadyOwner
}
