//go:build production && !server

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// singleInstanceOptions enforces single instance in production builds.
func singleInstanceOptions(onSecond func()) *application.SingleInstanceOptions {
	return &application.SingleInstanceOptions{
		UniqueID: "io.landrop.app",
		OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
			onSecond()
		},
	}
}
