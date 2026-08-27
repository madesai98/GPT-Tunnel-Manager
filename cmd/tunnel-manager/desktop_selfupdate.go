//go:build !nogui

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/selfupdate"
)

var managerSelfUpdate widget.Clickable

func (u *desktopUI) handleManagerSelfUpdate(gtx layout.Context) {
	for managerSelfUpdate.Clicked(gtx) {
		u.async("checking GPT Tunnel Manager update", func() error {
			executable, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve GPT Tunnel Manager executable: %w", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			plan, err := selfupdate.CheckAndStage(ctx, version, executable)
			if err != nil {
				return err
			}
			if !plan.UpdateAvailable {
				u.core.LogSelfUpdate("GPT Tunnel Manager is already on the latest published version", map[string]any{
					"current_version": plan.CurrentVersion,
					"latest_version":  plan.LatestVersion,
				})
				u.setMessage(fmt.Sprintf("GPT Tunnel Manager is already up to date (%s).", plan.CurrentVersion))
				return nil
			}

			u.core.LogSelfUpdate("GPT Tunnel Manager update staged", map[string]any{
				"current_version": plan.CurrentVersion,
				"latest_version":  plan.LatestVersion,
			})
			if err := selfupdate.Launch(plan, os.Getpid(), os.Args[1:]); err != nil {
				plan.Cleanup()
				return err
			}
			u.core.LogSelfUpdate("GPT Tunnel Manager updater terminal launched", map[string]any{
				"current_version": plan.CurrentVersion,
				"latest_version":  plan.LatestVersion,
			})
			u.setMessage(fmt.Sprintf("Updating GPT Tunnel Manager %s → %s…", plan.CurrentVersion, plan.LatestVersion))
			u.shutdownNow()
			return nil
		})
	}
}

func (u *desktopUI) managerSelfUpdateSection(gtx layout.Context) layout.Dimensions {
	current := strings.TrimSpace(version)
	if !strings.HasPrefix(current, "v") {
		current = "v" + current
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(material.H6(u.th, "GPT Tunnel Manager").Layout),
		layout.Rigid(material.Caption(u.th, "Current version: "+current).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx,
				material.Caption(u.th, "Checks the latest published GitHub release, stages it in a temporary directory, preserves config/data/tools, then restarts the manager through an independent updater terminal.").Layout,
			)
		}),
		layout.Rigid(material.Button(u.th, &managerSelfUpdate, "Update GPT Tunnel Manager").Layout),
	)
}
