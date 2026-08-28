//go:build !nogui

package main

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func managerIconButton(th *material.Theme, click *widget.Clickable, glyph string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := gtx.Dp(unit.Dp(34))
		gtx.Constraints = layout.Exact(image.Pt(size, size))
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			area := clip.Rect{Max: image.Pt(size, size)}.Push(gtx.Ops)
			defer area.Pop()
			pointer.CursorPointer.Add(gtx.Ops)

			bg := color.NRGBA{}
			if click.Hovered() {
				bg = uiSurfaceHover
			}
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Pt(size, size)
					return fillRounded(gtx, bg, unit.Dp(9))
				},
				func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						label := material.Body1(th, glyph)
						label.Color = uiText
						label.TextSize = unit.Sp(21)
						return label.Layout(gtx)
					})
				},
			)
		})
	}
}

func managerToggle(click *widget.Clickable, enabled bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		width := gtx.Dp(unit.Dp(46))
		height := gtx.Dp(unit.Dp(26))
		gtx.Constraints = layout.Exact(image.Pt(width, height))
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			area := clip.Rect{Max: image.Pt(width, height)}.Push(gtx.Ops)
			defer area.Pop()
			pointer.CursorPointer.Add(gtx.Ops)

			track := uiSurfaceHover
			if enabled {
				track = uiAccent
			} else if click.Hovered() {
				track = color.NRGBA{R: 50, G: 59, B: 75, A: 255}
			}

			radius := height / 2
			defer clip.UniformRRect(image.Rect(0, 0, width, height), radius).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, track)

			knobSize := gtx.Dp(unit.Dp(18))
			knobY := (height - knobSize) / 2
			knobX := gtx.Dp(unit.Dp(4))
			if enabled {
				knobX = width - knobSize - gtx.Dp(unit.Dp(4))
			}
			knob := image.Rect(knobX, knobY, knobX+knobSize, knobY+knobSize)
			defer clip.Ellipse(knob).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, color.NRGBA{R: 245, G: 247, B: 251, A: 255})
			return layout.Dimensions{Size: image.Pt(width, height)}
		})
	}
}
