// Command buslab 是虚拟总线实验室的桌面入口。
// 界面逻辑在 internal/ui，后端装配在 internal/bootstrap，本文件只做窗口与退出清理。
package main

import (
	"context"
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"

	"github.com/lansonsam/buslab/internal/bootstrap"
	"github.com/lansonsam/buslab/internal/host"
	"github.com/lansonsam/buslab/internal/orch"
	"github.com/lansonsam/buslab/internal/persist"
	"github.com/lansonsam/buslab/internal/ui"
)

func main() {
	detectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	h := host.New()
	report := h.Detect(detectCtx)
	cancel()

	orchestrator := orch.New(bootstrap.Registry(h, &report), report)
	settings := persist.LoadSettings()

	// 界面更新全部经 internal/ui 的事件泵用 fyne.Do 回主线程，声明后可关掉
	// Fyne 2.8 的线程兼容垫层与启动警告。
	app.SetMetadata(fyne.AppMetadata{
		ID:         "ai.factory.buslab",
		Name:       "虚拟总线实验室",
		Version:    "1.0.0",
		Build:      1,
		Migrations: map[string]bool{"fyneDo": true},
	})

	application := app.NewWithID("ai.factory.buslab")
	if settings.DarkTheme {
		application.Settings().SetTheme(theme.DarkTheme())
	}
	window := application.NewWindow("虚拟总线实验室")
	window.Resize(fyne.NewSize(1360, 840))

	gui := ui.New(orchestrator, window, ui.Options{
		Settings:     settings,
		CleanOrphans: bootstrap.OrphanCleaner(h),
	})
	window.SetContent(gui.Content())
	gui.Start()

	window.SetCloseIntercept(func() {
		gui.Stop()
		if err := orchestrator.Close(); err != nil {
			log.Printf("释放资源时出错：%v", err)
		}
		settings.LastProject = orchestrator.ProjectPath()
		if err := persist.SaveSettings(settings); err != nil {
			log.Printf("保存设置失败：%v", err)
		}
		window.Close()
	})

	window.ShowAndRun()
	// 兜底：窗口被外部强制关闭时不会触发 CloseIntercept，这里再尝试一次清理。
	if err := orchestrator.StopAll(); err != nil {
		log.Printf("退出清理未完成：%v", err)
	}
}
