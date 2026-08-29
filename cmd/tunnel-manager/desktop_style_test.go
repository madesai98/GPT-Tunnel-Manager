//go:build !nogui

package main

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func TestFillSurfacePreservesInnerHorizontalInset(t *testing.T) {
	var ops op.Ops
	gtx := layout.Context{
		Ops: &ops,
		Constraints: layout.Constraints{
			Max: image.Pt(300, 100),
		},
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}

	innerMinX, innerMaxX := -1, -1
	widget := fillSurface(
		uiSurface,
		unit.Dp(8),
		layout.Inset{Left: unit.Dp(10), Right: unit.Dp(20)},
		func(gtx layout.Context) layout.Dimensions {
			innerMinX = gtx.Constraints.Min.X
			innerMaxX = gtx.Constraints.Max.X
			return layout.Dimensions{Size: image.Pt(40, 20)}
		},
	)

	dims := widget(gtx)
	if dims.Size.X != 300 {
		t.Fatalf("fillSurface width = %d, want 300", dims.Size.X)
	}
	if innerMinX != 0 {
		t.Fatalf("inner minimum width = %d, want 0", innerMinX)
	}
	if innerMaxX != 270 {
		t.Fatalf("inner maximum width = %d, want 270 after horizontal inset", innerMaxX)
	}
}
