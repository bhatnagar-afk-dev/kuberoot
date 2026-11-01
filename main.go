// Command kuberoot is the KubeRoot desktop application.
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/bhatnagar-afk-dev/kuberoot/app"
	"github.com/bhatnagar-afk-dev/kuberoot/internal/settings"
	"github.com/bhatnagar-afk-dev/kuberoot/internal/shellenv"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Must run before anything reads PATH or KUBECONFIG.
	if res, err := shellenv.Apply(context.Background()); err != nil {
		slog.Warn("could not resolve login shell environment; exec credential plugins may not be found", "shell", res.Shell, "err", err)
	} else if !res.Skipped {
		slog.Info("adopted login shell environment", "shell", res.Shell, "vars", res.Applied)
	}

	st, err := openSettings()
	if err != nil {
		log.Fatal(err)
	}

	a := application.New(application.Options{
		Name:        "KubeRoot",
		Description: "Open-source cross-platform Kubernetes desktop client",
		Services: []application.Service{
			application.NewService(&app.SystemService{}),
			application.NewService(app.NewClusterService(st)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	a.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "KubeRoot",
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 600,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(17, 19, 24),
		URL:              "/",
	})

	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}

// openSettings opens the settings file. A corrupt file is moved aside so a
// bad write never stops the app from starting.
func openSettings() (*settings.Store, error) {
	path, err := settings.DefaultPath()
	if err != nil {
		return nil, err
	}
	st, err := settings.Open(path)
	if err == nil {
		return st, nil
	}
	backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
	if rerr := os.Rename(path, backup); rerr != nil {
		return nil, fmt.Errorf("%w (and could not move it aside: %w)", err, rerr)
	}
	slog.Warn("settings file was unreadable; starting with defaults", "err", err, "backup", backup)
	return settings.Open(path)
}
