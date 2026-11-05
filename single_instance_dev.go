//go:build !production && !server

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// In development mode (wails3 dev), disable single instance enforcement so
// hot-reloading, dev rebuilds, and watcher restarts are never blocked by
// existing background instances.
func singleInstanceOptions(onSecond func()) *application.SingleInstanceOptions {
	return nil
}
