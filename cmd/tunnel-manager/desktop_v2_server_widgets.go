//go:build !nogui

package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func v2ToggledServerMode(mode v2config.ServerMode) v2config.ServerMode {
	if mode == v2config.ModeDisabled {
		return v2config.ModeManaged
	}
	return v2config.ModeDisabled
}

func v2IdleCountdown(th *material.Theme, deadline *time.Time, timeoutSeconds int, visible bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !visible || deadline == nil || timeoutSeconds <= 0 {
			return layout.Dimensions{}
		}
		total := time.Duration(timeoutSeconds) * time.Second
		remaining := deadline.Sub(gtx.Now)
		if remaining < 0 {
			remaining = 0
		}
		progress := float32(remaining) / float32(total)
		if progress < 0 {
			progress = 0
		} else if progress > 1 {
			progress = 1
		}
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(250 * time.Millisecond)})

		size := gtx.Dp(unit.Dp(66))
		gtx.Constraints = layout.Exact(image.Pt(size, size))
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				drawV2IdleProgress(gtx, size, progress)
				return layout.Dimensions{Size: image.Pt(size, size)}
			},
			func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					label := material.Body1(th, formatV2IdleRemaining(remaining))
					label.Color = uiText
					label.TextSize = unit.Sp(10)
					return label.Layout(gtx)
				})
			},
		)
	}
}

func formatV2IdleRemaining(remaining time.Duration) string {
	if remaining <= 0 {
		return "0s"
	}
	seconds := int64((remaining + time.Second - 1) / time.Second)
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%dm%ds", hours, minutes, secs)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, secs)
	}
	return fmt.Sprintf("%ds", secs)
}

func drawV2IdleProgress(gtx layout.Context, size int, progress float32) {
	stroke := float32(gtx.Dp(unit.Dp(3)))
	inset := gtx.Dp(unit.Dp(5))
	bounds := image.Rect(inset, inset, size-inset, size-inset)
	paint.FillShape(gtx.Ops, uiSurfaceHover, clip.Stroke{
		Path:  clip.Ellipse(bounds).Path(gtx.Ops),
		Width: stroke,
	}.Op())
	if progress <= 0 {
		return
	}

	center := f32.Pt(float32(size)/2, float32(size)/2)
	radius := float32(bounds.Dx()) / 2
	startAngle := -math.Pi / 2
	sin, cos := math.Sincos(startAngle)
	start := center.Add(f32.Pt(float32(cos)*radius, float32(sin)*radius))
	angle := float32(-2 * math.Pi * float64(progress))
	if angle <= -2*math.Pi {
		angle = float32(-2*math.Pi + 0.001)
	}

	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(start)
	path.ArcTo(center, center, angle)
	paint.FillShape(gtx.Ops, uiAccent, clip.Stroke{Path: path.End(), Width: stroke}.Op())
}

func v2CompactIconButton(th *material.Theme, click *widget.Clickable, glyph string, hoverBG, fg color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := gtx.Dp(unit.Dp(34))
		gtx.Constraints = layout.Exact(image.Pt(size, size))
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			area := clip.Rect{Max: image.Pt(size, size)}.Push(gtx.Ops)
			defer area.Pop()
			pointer.CursorPointer.Add(gtx.Ops)

			bg := color.NRGBA{}
			if click.Hovered() {
				bg = hoverBG
			}
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Pt(size, size)
					return fillRounded(gtx, bg, unit.Dp(9))
				},
				func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						label := material.Body1(th, glyph)
						label.Color = fg
						label.TextSize = unit.Sp(21)
						return label.Layout(gtx)
					})
				},
			)
		})
	}
}

func v2ServerIconButton(th *material.Theme, click *widget.Clickable, glyph string) layout.Widget {
	return v2CompactIconButton(th, click, glyph, uiSurfaceHover, uiText)
}

func v2DangerIconButton(th *material.Theme, click *widget.Clickable, glyph string) layout.Widget {
	return v2CompactIconButton(th, click, glyph, uiDangerSoft, uiDanger)
}

func v2ServerToggle(click *widget.Clickable, enabled bool) layout.Widget {
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
