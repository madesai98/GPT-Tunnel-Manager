//go:build !nogui

package main

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

var (
	v2ChromeClose    widget.Clickable
	v2ChromeMinimize widget.Clickable
	v2ChromeMaximize widget.Clickable
)

const v2ChromeHeight = unit.Dp(36)

func v2FillCircle(gtx layout.Context, bounds image.Rectangle, col color.NRGBA) {
	defer clip.Ellipse(bounds).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
}

func v2TrafficLight(gtx layout.Context, button *widget.Clickable, col color.NRGBA) layout.Dimensions {
	size := gtx.Dp(unit.Dp(22))
	gtx.Constraints = layout.Exact(image.Pt(size, size))
	return button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := layout.Dimensions{Size: image.Pt(size, size)}
		if button.Hovered() {
			glow := col
			glow.A = 72
			v2FillCircle(gtx, image.Rect(0, 0, size, size), glow)
			innerGlow := col
			innerGlow.A = 128
			inset := gtx.Dp(unit.Dp(2))
			v2FillCircle(gtx, image.Rect(inset, inset, size-inset, size-inset), innerGlow)
		}
		inset := gtx.Dp(unit.Dp(5))
		v2FillCircle(gtx, image.Rect(inset, inset, size-inset, size-inset), col)
		return dims
	})
}

func v2ChromeDragRegion(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min = gtx.Constraints.Max
	dims := layout.Dimensions{Size: gtx.Constraints.Max}
	stack := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
	pointer.CursorGrab.Add(gtx.Ops)
	stack.Pop()
	return dims
}

func (u *v2DesktopUI) perform(action system.Action) {
	if win := u.currentWindow(); win != nil {
		win.Perform(action)
	}
}

func (u *v2DesktopUI) v2TitleBar(gtx layout.Context) layout.Dimensions {
	for v2ChromeClose.Clicked(gtx) {
		u.perform(system.ActionClose)
	}
	for v2ChromeMinimize.Clicked(gtx) {
		u.perform(system.ActionMinimize)
	}
	for v2ChromeMaximize.Clicked(gtx) {
		if u.deco.Maximized {
			u.perform(system.ActionUnmaximize)
		} else {
			u.perform(system.ActionMaximize)
		}
	}

	h := gtx.Dp(v2ChromeHeight)
	if h > gtx.Constraints.Max.Y {
		h = gtx.Constraints.Max.Y
	}
	if h <= 0 || gtx.Constraints.Max.X <= 0 {
		return layout.Dimensions{}
	}

	red := color.NRGBA{R: 255, G: 95, B: 87, A: 255}
	yellow := color.NRGBA{R: 254, G: 188, B: 46, A: 255}
	green := color.NRGBA{R: 40, G: 200, B: 64, A: 255}

	record := op.Record(gtx.Ops)
	overlay := gtx
	overlay.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	layout.Flex{Axis: layout.Horizontal}.Layout(overlay,
		layout.Flexed(1, v2ChromeDragRegion),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2TrafficLight(gtx, &v2ChromeMinimize, yellow) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(4)}.Layout(gtx) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2TrafficLight(gtx, &v2ChromeMaximize, green) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(4)}.Layout(gtx) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2TrafficLight(gtx, &v2ChromeClose, red) }),
				)
			})
		}),
	)
	call := record.Stop()
	op.Defer(gtx.Ops, call)

	// Keep the custom chrome as an overlay so the application content still
	// starts at the physical top edge of the frameless window, matching v1.
	return layout.Dimensions{}
}
