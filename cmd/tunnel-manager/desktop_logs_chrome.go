//go:build !nogui

package main

import (
	"encoding/json"
	"image"
	"image/color"
	"strings"

	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

var (
	chromeClose    widget.Clickable
	chromeMinimize widget.Clickable
	chromeMaximize widget.Clickable

	logPaneResize        gesture.Drag
	logPaneHeight        = unit.Dp(285)
	logPaneResizeAnchorY float32
	logPaneMinHeight     = unit.Dp(190)
	logPaneMaxHeight     = unit.Dp(285)
)

const chromeHeight = unit.Dp(36)

func fillCircle(gtx layout.Context, bounds image.Rectangle, col color.NRGBA) {
	defer clip.Ellipse(bounds).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
}

func trafficLight(gtx layout.Context, button *widget.Clickable, col color.NRGBA) layout.Dimensions {
	size := gtx.Dp(unit.Dp(22))
	gtx.Constraints = layout.Exact(image.Pt(size, size))
	return button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := layout.Dimensions{Size: image.Pt(size, size)}
		if button.Hovered() {
			glow := col
			glow.A = 72
			fillCircle(gtx, image.Rect(0, 0, size, size), glow)
			innerGlow := col
			innerGlow.A = 128
			inset := gtx.Dp(unit.Dp(2))
			fillCircle(gtx, image.Rect(inset, inset, size-inset, size-inset), innerGlow)
		}
		inset := gtx.Dp(unit.Dp(5))
		fillCircle(gtx, image.Rect(inset, inset, size-inset, size-inset), col)
		return dims
	})
}

func chromeDragRegion(_ color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = gtx.Constraints.Max
		dims := layout.Dimensions{Size: gtx.Constraints.Max}
		stack := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
		system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
		pointer.CursorGrab.Add(gtx.Ops)
		stack.Pop()
		return dims
	}
}

func (u *desktopUI) titleBar(gtx layout.Context) layout.Dimensions {
	for chromeClose.Clicked(gtx) {
		u.requestClose()
	}
	for chromeMinimize.Clicked(gtx) {
		u.hideToTray()
	}
	for chromeMaximize.Clicked(gtx) {
		if u.deco.Maximized {
			u.perform(system.ActionUnmaximize)
		} else {
			u.perform(system.ActionMaximize)
		}
	}

	h := gtx.Dp(chromeHeight)
	if h > gtx.Constraints.Max.Y {
		h = gtx.Constraints.Max.Y
	}
	if h <= 0 || gtx.Constraints.Max.X <= 0 {
		return layout.Dimensions{}
	}

	red := color.NRGBA{R: 255, G: 95, B: 87, A: 255}
	yellow := color.NRGBA{R: 254, G: 188, B: 46, A: 255}
	green := color.NRGBA{R: 40, G: 200, B: 64, A: 255}

	macro := op.Record(gtx.Ops)
	overlay := gtx
	overlay.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	layout.Flex{Axis: layout.Horizontal}.Layout(overlay,
		layout.Flexed(1, chromeDragRegion(uiCanvas)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return trafficLight(gtx, &chromeMinimize, yellow) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(4)}.Layout(gtx) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return trafficLight(gtx, &chromeMaximize, green) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(4)}.Layout(gtx) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return trafficLight(gtx, &chromeClose, red) }),
				)
			})
		}),
	)
	call := macro.Stop()
	op.Defer(gtx.Ops, call)

	// The native chrome is an overlay. Returning zero height lets the sidebar
	// and page content begin at the actual top edge of the frameless window.
	return layout.Dimensions{}
}

func (u *desktopUI) logPaneResizeHandle(gtx layout.Context) layout.Dimensions {
	var (
		latestDragY float32
		haveDrag    bool
	)
	for {
		ev, ok := logPaneResize.Update(gtx.Metric, gtx.Source, gesture.Vertical)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			// Pointer positions are local to the handle. Keep the press offset
			// fixed and correct the pane height by the local error each frame;
			// this pins the moving handle to the pointer instead of measuring
			// against a coordinate system that moves with the pane.
			logPaneResizeAnchorY = ev.Position.Y
		case pointer.Drag:
			latestDragY = ev.Position.Y
			haveDrag = true
		}
	}

	if haveDrag {
		pxPerDp := gtx.Metric.PxPerDp
		if pxPerDp <= 0 {
			pxPerDp = 1
		}
		correction := unit.Dp((logPaneResizeAnchorY - latestDragY) / pxPerDp)
		next := logPaneHeight + correction
		if next < logPaneMinHeight {
			next = logPaneMinHeight
		}
		if next > logPaneMaxHeight {
			next = logPaneMaxHeight
		}
		if next != logPaneHeight {
			logPaneHeight = next
			u.invalidate()
		}
	}

	h := gtx.Dp(unit.Dp(10))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h
	dims := layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h)}
	stack := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	logPaneResize.Add(gtx.Ops)
	pointer.CursorRowResize.Add(gtx.Ops)
	stack.Pop()

	lineY := gtx.Dp(unit.Dp(1))
	lineH := gtx.Dp(unit.Dp(2))
	line := image.Rect(0, lineY, dims.Size.X, lineY+lineH)
	defer clip.Rect(line).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, uiSurfaceHover)
	return dims
}

func pxToDp(gtx layout.Context, px int) unit.Dp {
	pxPerDp := gtx.Metric.PxPerDp
	if pxPerDp <= 0 {
		pxPerDp = 1
	}
	return unit.Dp(float32(px) / pxPerDp)
}

func (u *desktopUI) expandedLogPane(gtx layout.Context, header layout.Widget) layout.Dimensions {
	minHeight := gtx.Dp(unit.Dp(190))
	maxHeight := gtx.Constraints.Max.Y - gtx.Dp(unit.Dp(150))
	if maxHeight < minHeight {
		maxHeight = gtx.Constraints.Max.Y
	}
	logPaneMinHeight = pxToDp(gtx, minHeight)
	logPaneMaxHeight = pxToDp(gtx, maxHeight)

	height := gtx.Dp(logPaneHeight)
	if height < minHeight {
		height = minHeight
		logPaneHeight = logPaneMinHeight
	}
	if height > maxHeight {
		height = maxHeight
		logPaneHeight = logPaneMaxHeight
	}
	gtx.Constraints.Min.Y = height
	gtx.Constraints.Max.Y = height

	return surface(uiSurface, unit.Dp(12), layout.Inset{Bottom: unit.Dp(14), Left: unit.Dp(14), Right: unit.Dp(14)}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.logPaneResizeHandle),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(3)}.Layout(gtx, header)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
			layout.Flexed(1, u.modernLogPage),
		)
	})(gtx)
}

type logViewRow struct {
	time      string
	level     string
	source    string
	component string
	message   string
}

func fixedLogCell(width unit.Dp, child layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		w := gtx.Dp(width)
		gtx.Constraints.Min.X = w
		gtx.Constraints.Max.X = w
		return child(gtx)
	}
}

func logText(th *material.Theme, value string, col color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := material.Caption(th, value)
		label.Color = col
		return label.Layout(gtx)
	}
}

func logLevelColors(level string) (color.NRGBA, color.NRGBA) {
	switch strings.ToUpper(level) {
	case "TRACE":
		return uiSurfaceHover, uiFaint
	case "DEBUG":
		return uiAccentSoft, color.NRGBA{R: 157, G: 174, B: 255, A: 255}
	case "INFO":
		return color.NRGBA{R: 25, G: 64, B: 70, A: 255}, color.NRGBA{R: 91, G: 216, B: 215, A: 255}
	case "WARN":
		return uiWarningSoft, uiWarning
	case "ERROR":
		return uiDangerSoft, uiDanger
	default:
		return uiSurfaceHover, uiMuted
	}
}

func logLevelBadge(th *material.Theme, level string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		bg, fg := logLevelColors(level)
		return surface(bg, unit.Dp(20), layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3), Left: unit.Dp(7), Right: unit.Dp(7)}, func(gtx layout.Context) layout.Dimensions {
			label := material.Caption(th, strings.ToUpper(level))
			label.Color = fg
			return label.Layout(gtx)
		})(gtx)
	}
}

func (u *desktopUI) logGridRow(gtx layout.Context, row logViewRow, header bool) layout.Dimensions {
	textColor := uiMuted
	messageColor := uiText
	if header {
		textColor = uiFaint
		messageColor = uiFaint
	}
	levelWidget := logLevelBadge(u.th, row.level)
	if header {
		levelWidget = logText(u.th, row.level, textColor)
	}
	return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(fixedLogCell(unit.Dp(76), logText(u.th, row.time, textColor))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
			layout.Rigid(fixedLogCell(unit.Dp(68), levelWidget)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
			layout.Rigid(fixedLogCell(unit.Dp(116), logText(u.th, row.source, textColor))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
			layout.Rigid(fixedLogCell(unit.Dp(132), logText(u.th, row.component, textColor))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
			layout.Flexed(1, logText(u.th, row.message, messageColor)),
		)
	})
}

func (u *desktopUI) modernLogPage(gtx layout.Context) layout.Dimensions {
	levels := []string{"All", "TRACE", "DEBUG", "INFO", "WARN", "ERROR"}
	for u.levelBtn.Clicked(gtx) {
		u.logLevel = (u.logLevel + 1) % len(levels)
		u.set.displayMode = u.logLevel
		u.persistLogDisplayLevel(displayLevelValues[u.logLevel])
	}
	for u.clearLogs.Clicked(gtx) {
		u.core.ClearLogs()
	}
	for u.exportText.Clicked(gtx) {
		u.async("exporting text logs", func() error {
			path, err := u.core.ExportLogs("text")
			if err == nil {
				u.setMessage("Exported logs: " + path)
			}
			return err
		})
	}
	for u.exportJSON.Clicked(gtx) {
		u.async("exporting JSONL logs", func() error {
			path, err := u.core.ExportLogs("jsonl")
			if err == nil {
				u.setMessage("Exported logs: " + path)
			}
			return err
		})
	}

	query := strings.ToLower(strings.TrimSpace(u.logSearch.Text()))
	filtered := make([]logViewRow, 0)
	for _, event := range u.core.Logs() {
		level := strings.ToUpper(string(event.Level))
		if u.logLevel > 0 && level != levels[u.logLevel] {
			continue
		}
		message := event.Message
		if len(event.Fields) != 0 {
			if fields, err := json.Marshal(event.Fields); err == nil {
				message += " " + string(fields)
			}
		}
		row := logViewRow{
			time:      event.Timestamp.Format("15:04:05"),
			level:     level,
			source:    event.Source,
			component: event.Component,
			message:   message,
		}
		searchable := strings.ToLower(strings.Join([]string{row.time, row.level, row.source, row.component, row.message}, " "))
		if query == "" || strings.Contains(searchable, query) {
			filtered = append(filtered, row)
		}
	}

	u.logs.List.ScrollToEnd = true
	header := logViewRow{time: "TIME", level: "LEVEL", source: "SOURCE", component: "COMPONENT", message: "MESSAGE / FIELDS"}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, inputSurface(func(gtx layout.Context) layout.Dimensions {
					style := material.Editor(u.th, &u.logSearch, "Search logs")
					style.Color = uiText
					style.HintColor = uiFaint
					return style.Layout(gtx)
				})),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &u.levelBtn, "Level: "+levels[u.logLevel])),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &u.exportText, "Text")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &u.exportJSON, "JSONL")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(dangerButton(u.th, &u.clearLogs, "Clear")),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(7)}.Layout(gtx) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return surface(uiCanvas, unit.Dp(7), layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5)}, func(gtx layout.Context) layout.Dimensions {
				return u.logGridRow(gtx, header, true)
			})(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(4)}.Layout(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return u.logs.Layout(gtx, len(filtered), func(gtx layout.Context, i int) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, surface(uiSurfaceRaised, unit.Dp(7), layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}, func(gtx layout.Context) layout.Dimensions {
					return u.logGridRow(gtx, filtered[i], false)
				}))
			})
		}),
	)
}
