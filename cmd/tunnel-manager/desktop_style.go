//go:build !nogui

package main

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

var (
	uiCanvas        = color.NRGBA{R: 14, G: 17, B: 23, A: 255}
	uiSidebar       = color.NRGBA{R: 18, G: 22, B: 30, A: 255}
	uiSurface       = color.NRGBA{R: 24, G: 29, B: 39, A: 255}
	uiSurfaceRaised = color.NRGBA{R: 31, G: 37, B: 49, A: 255}
	uiSurfaceHover  = color.NRGBA{R: 38, G: 45, B: 59, A: 255}
	uiAccent        = color.NRGBA{R: 96, G: 125, B: 255, A: 255}
	uiAccentSoft    = color.NRGBA{R: 40, G: 51, B: 88, A: 255}
	uiText          = color.NRGBA{R: 238, G: 241, B: 247, A: 255}
	uiMuted         = color.NRGBA{R: 145, G: 154, B: 172, A: 255}
	uiFaint         = color.NRGBA{R: 94, G: 103, B: 121, A: 255}
	uiSuccess       = color.NRGBA{R: 78, G: 207, B: 151, A: 255}
	uiSuccessSoft   = color.NRGBA{R: 27, G: 67, B: 55, A: 255}
	uiWarning       = color.NRGBA{R: 241, G: 184, B: 76, A: 255}
	uiWarningSoft   = color.NRGBA{R: 70, G: 57, B: 29, A: 255}
	uiDanger        = color.NRGBA{R: 239, G: 105, B: 115, A: 255}
	uiDangerSoft    = color.NRGBA{R: 73, G: 34, B: 42, A: 255}
)

func fillRounded(gtx layout.Context, col color.NRGBA, radius unit.Dp) layout.Dimensions {
	r := gtx.Dp(radius)
	defer clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, r).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
	return layout.Dimensions{Size: gtx.Constraints.Min}
}

func surface(bg color.NRGBA, radius unit.Dp, inset layout.Inset, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions { return fillRounded(gtx, bg, radius) },
			func(gtx layout.Context) layout.Dimensions { return inset.Layout(gtx, content) },
		)
	}
}

func fillSurface(bg color.NRGBA, radius unit.Dp, inset layout.Inset, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if gtx.Constraints.Max.X > 0 {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
		}
		return surface(bg, radius, inset, content)(gtx)
	}
}

func card(content layout.Widget) layout.Widget {
	return fillSurface(uiSurface, unit.Dp(14), layout.UniformInset(unit.Dp(18)), content)
}

func compactCard(content layout.Widget) layout.Widget {
	return fillSurface(uiSurface, unit.Dp(12), layout.UniformInset(unit.Dp(14)), content)
}

func inputSurface(content layout.Widget) layout.Widget {
	return fillSurface(uiSurfaceRaised, unit.Dp(9), layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(10), Right: unit.Dp(10)}, content)
}

func styledButton(th *material.Theme, click *widget.Clickable, label string, bg, fg color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		style := material.Button(th, click, label)
		style.Background = bg
		style.Color = fg
		style.CornerRadius = unit.Dp(9)
		style.Inset = layout.Inset{Top: unit.Dp(9), Bottom: unit.Dp(9), Left: unit.Dp(13), Right: unit.Dp(13)}
		return style.Layout(gtx)
	}
}

func primaryButton(th *material.Theme, click *widget.Clickable, label string) layout.Widget {
	return styledButton(th, click, label, uiAccent, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
}

func secondaryButton(th *material.Theme, click *widget.Clickable, label string) layout.Widget {
	return styledButton(th, click, label, uiSurfaceRaised, uiText)
}

func dangerButton(th *material.Theme, click *widget.Clickable, label string) layout.Widget {
	return styledButton(th, click, label, uiDangerSoft, uiDanger)
}

func navButton(th *material.Theme, click *widget.Clickable, label string, selected bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		bg := uiSidebar
		fg := uiMuted
		if selected {
			bg = uiAccentSoft
			fg = uiText
		}
		style := material.Button(th, click, label)
		style.Background = bg
		style.Color = fg
		style.CornerRadius = unit.Dp(10)
		style.Inset = layout.Inset{Top: unit.Dp(11), Bottom: unit.Dp(11), Left: unit.Dp(14), Right: unit.Dp(14)}
		return style.Layout(gtx)
	}
}

func pill(th *material.Theme, text string, bg, fg color.NRGBA) layout.Widget {
	return surface(bg, unit.Dp(20), layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(9), Right: unit.Dp(9)}, func(gtx layout.Context) layout.Dimensions {
		label := material.Caption(th, text)
		label.Color = fg
		return label.Layout(gtx)
	})
}

func mutedCaption(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := material.Caption(th, text)
		label.Color = uiMuted
		return label.Layout(gtx)
	}
}

func faintCaption(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := material.Caption(th, text)
		label.Color = uiFaint
		return label.Layout(gtx)
	}
}

func sectionTitle(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := material.H6(th, text)
		label.Color = uiText
		return label.Layout(gtx)
	}
}

func pageTitle(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := material.H4(th, text)
		label.TextSize = unit.Sp(27)
		label.Color = uiText
		return label.Layout(gtx)
	}
}

func stateColors(state string, ready bool) (color.NRGBA, color.NRGBA, string) {
	if ready {
		return uiSuccessSoft, uiSuccess, "READY"
	}
	switch state {
	case "starting", "restarting", "stopping":
		return uiWarningSoft, uiWarning, "WORKING"
	case "degraded", "failed", "error":
		return uiDangerSoft, uiDanger, "DEGRADED"
	default:
		return uiSurfaceRaised, uiMuted, "STOPPED"
	}
}
