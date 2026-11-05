//go:build !server

package main

import (
	"embed"
	_ "embed"
	"log"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/CyanHwang/lan-drop/internal/i18n"
	"github.com/CyanHwang/lan-drop/internal/transfer"
)

// Wails embeds the desktop frontend. The phone/browser UI is embedded
// separately inside internal/transfer, because it is served over plain HTTP
// rather than through the WebView asset handler.
//
//go:embed all:frontend/dist
var assets embed.FS

// System tray icon embedded directly from the app build assets.
//
//go:embed build/appicon.png
var appIconPNG []byte

// Window chrome colours, kept in sync with --bg in frontend/public/style.css.
var (
	darkBG  = application.NewRGB(6, 7, 15)
	lightBG = application.NewRGB(246, 247, 249)
)

func bgColour(dark bool) application.RGBA {
	if dark {
		return darkBG
	}
	return lightBG
}

func init() {
	// Registered so the binding generator emits typed JS helpers for them.
	application.RegisterEvent[transfer.Progress]("transfer:progress")
	application.RegisterEvent[string]("server:error")
}

func main() {
	svc := NewAppService()

	var (
		app       *application.App
		mainWin   *application.WebviewWindow
		isExiting uint32
	)

	// presentMainWindow brings the main window to the front without destroying
	// or recreating it. This guarantees zero-latency wakeup and prevents any
	// WebView2 process thrashing or race conditions.
	presentMainWindow := func() {
		if mainWin != nil {
			mainWin.Show()
			mainWin.Restore()
			mainWin.Focus()
		}
	}

	app = application.New(application.Options{
		Name:        "Lan Drop",
		Description: i18n.T(svc.L(), i18n.AppDescription),
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		// Single instance enforcement: active in production builds; disabled in dev mode.
		SingleInstance: singleInstanceOptions(presentMainWindow),
		// Multi-platform background behaviour:
		// Windows & Linux: closing the window does not terminate the process;
		// the LAN server and system tray remain active in the background.
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		// macOS: windows close without quitting the application by standard convention.
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	svc.attach(app)

	// The quit button in the UI and the tray menu share one path: flag the
	// exit first, so the WindowClosing hook lets the window close for real.
	svc.setQuitHook(func() {
		atomic.StoreUint32(&isExiting, 1)
		app.Quit()
	})

	// Create the main window.
	mainWin = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Lan Drop",
		Width:     1280,
		Height:    720,
		MinWidth:  800,
		MinHeight: 600,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: bgColour(app.Env.IsDarkMode()),
		URL:              "/",
	})

	// Intercept the close event (clicking the X button): hide to tray instead
	// of destroying the window, unless the user explicitly requested Exit.
	mainWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if atomic.LoadUint32(&isExiting) == 1 {
			// User clicked "Quit Lan Drop" in the tray menu; proceed with real exit.
			return
		}
		// Intercept the close request and hide the window to the tray.
		e.Cancel()
		mainWin.Hide()
	})

	// Keep the window surface colour matching the system theme.
	app.Event.OnApplicationEvent(events.Common.ThemeChanged, func(e *application.ApplicationEvent) {
		if mainWin != nil {
			mainWin.SetBackgroundColour(bgColour(e.Context().IsDarkMode()))
		}
	})

	// macOS: restore window when user clicks the Dock icon.
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(e *application.ApplicationEvent) {
		presentMainWindow()
	})

	// System tray setup:
	// - Left click: restore the window.
	// - Left double-click: restore the window (standard Windows desktop convention).
	// - Right click menu: "Open Lan Drop" + separator + "Quit Lan Drop".
	// The menu is rebuilt whenever the UI language changes.
	tray := app.SystemTray.New()
	tray.SetIcon(appIconPNG)

	rebuildTrayMenu := func(l i18n.Lang) {
		tray.SetTooltip(i18n.T(l, i18n.TrayTooltip))
		m := application.NewMenu()
		m.Add(i18n.T(l, i18n.TrayOpen)).OnClick(func(*application.Context) {
			presentMainWindow()
		})
		m.AddSeparator()
		m.Add(i18n.T(l, i18n.TrayQuit)).OnClick(func(*application.Context) {
			atomic.StoreUint32(&isExiting, 1)
			app.Quit()
		})
		tray.SetMenu(m)
	}

	svc.setLangChangeHook(rebuildTrayMenu)
	rebuildTrayMenu(svc.L())

	tray.OnClick(func() {
		presentMainWindow()
	})
	tray.OnDoubleClick(func() {
		presentMainWindow()
	})
	tray.Run()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
