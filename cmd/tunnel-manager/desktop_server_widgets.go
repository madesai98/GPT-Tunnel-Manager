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

	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
)

func idleCountdown(th *material.Theme, snapshot servers.Snapshot) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !snapshot.Ready || !snapshot.IdleShutdownEnabled || snapshot.IdleDeadlineAt == nil || snapshot.IdleTimeoutSeconds <= 0 {
			return layout.Dimensions{}
		}

		total := time.Duration(snapshot.IdleTimeoutSeconds) * time.Second
		remaining := snapshot.IdleDeadlineAt.Sub(gtx.Now)
		if remaining < 0 {
			remaining = 0
		}
		progress := float32(remaining) / float32(total)
		if progress < 0 {
			progress = 0
		} else if progress > 1 {
			progress = 1
		}

		// Keep the ring smooth while the countdown text changes only when the
		// rounded-up remaining second changes.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(250 * time.Millisecond)})

		size := gtx.Dp(unit.Dp(66))
		gtx.Constraints = layout.Exact(image.Pt(size, size))
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				drawIdleProgress(gtx, size, progress)
				return layout.Dimensions{Size: image.Pt(size, size)}
			},
			func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					label := material.Body1(th, formatIdleRemaining(remaining))
					label.Color = uiText
					label.TextSize = unit.Sp(10)
					return label.Layout(gtx)
				})
			},
		)
	}
}

func formatIdleRemaining(remaining time.Duration) string {
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

func drawIdleProgress(gtx layout.Context, size int, progress float32) {
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
	// A mathematically exact 2π arc has the same start/end point and can be
	// treated as degenerate by path renderers. Keep the full state visually
	// indistinguishable while avoiding that edge case.
	if angle <= -2*math.Pi {
		angle = float32(-2*math.Pi + 0.001)
	}

	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(start)
	path.ArcTo(center, center, angle)
	paint.FillShape(gtx.Ops, uiAccent, clip.Stroke{
		Path:  path.End(),
		Width: stroke,
	}.Op())
}

func copyIconButton(th *material.Theme, click *widget.Clickable) layout.Widget {
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
					drawCopyIcon(gtx)
					return layout.Dimensions{Size: image.Pt(size, size)}
				},
			)
		})
	}
}

func drawCopyIcon(gtx layout.Context) {
	px := func(v unit.Dp) int { return gtx.Dp(v) }
	stroke := float32(px(unit.Dp(1.6)))
	if stroke < 1 {
		stroke = 1
	}

	back := clip.UniformRRect(
		image.Rect(px(unit.Dp(12)), px(unit.Dp(7)), px(unit.Dp(26)), px(unit.Dp(21))),
		px(unit.Dp(2)),
	)
	front := clip.UniformRRect(
		image.Rect(px(unit.Dp(8)), px(unit.Dp(11)), px(unit.Dp(22)), px(unit.Dp(25))),
		px(unit.Dp(2)),
	)
	paint.FillShape(gtx.Ops, uiText, clip.Stroke{Path: back.Path(gtx.Ops), Width: stroke}.Op())
	paint.FillShape(gtx.Ops, uiText, clip.Stroke{Path: front.Path(gtx.Ops), Width: stroke}.Op())
}
