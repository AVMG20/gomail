//go:build !linux

package main

// trayAvailable: macOS and Windows always have a menu bar / notification area.
func trayAvailable() bool { return true }

// singleInstanceUsable: Windows (named mutex) and macOS need no D-Bus.
func singleInstanceUsable(string) bool { return true }
