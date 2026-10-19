//go:build !linux

package main

// trayAvailable: macOS and Windows always have a menu bar / notification area.
func trayAvailable() bool { return true }

// sessionBusAvailable: single-instance mode needs no D-Bus outside Linux.
func sessionBusAvailable() bool { return true }
