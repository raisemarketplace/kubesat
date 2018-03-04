package kit

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func checkGridLayout(t *testing.T, width, height int, areas map[string]Area, rects map[string]Rect) {
	grid := NewGrid(areas)
	layout := grid.Layout(width, height)

	errs := make([]string, 0)
	for name, rect := range layout {
		if !reflect.DeepEqual(rects[name], rect) {
			msg := fmt.Sprintf("expected rect[%s]:%s but was: %s",
				name, rects[name], rect)
			errs = append(errs, msg)
		}
	}
	if len(errs) > 0 {
		t.Fatalf("%s", strings.Join(errs, "\n    "))
	}
}

func TestGridLayoutAbsoluteFill(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["all"] = AreaAt(0, 0).Span(1, 1).WidthCh(80).HeightCh(25)
	rects["all"] = Rect{X: 0, Y: 0, Width: 80, Height: 25}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutAbsoluteOverflow(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["overflow"] = AreaAt(0, 0).Span(1, 1).WidthCh(100).HeightCh(100)
	rects["overflow"] = Rect{X: 0, Y: 0, Width: 100, Height: 100}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutFourAbsolute(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["topleft"] = AreaAt(0, 0).Span(1, 1).WidthCh(40).HeightCh(12)
	areas["topright"] = AreaAt(1, 0).Span(1, 1).WidthCh(40).HeightCh(12)
	areas["botleft"] = AreaAt(0, 1).Span(1, 1).WidthCh(40).HeightCh(13)
	areas["botright"] = AreaAt(1, 1).Span(1, 1).WidthCh(40).HeightCh(13)

	rects["topleft"] = Rect{X: 0, Y: 0, Width: 40, Height: 12}
	rects["topright"] = Rect{X: 40, Y: 0, Width: 40, Height: 12}
	rects["botleft"] = Rect{X: 0, Y: 12, Width: 40, Height: 13}
	rects["botright"] = Rect{X: 40, Y: 12, Width: 40, Height: 13}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutFractionalFill(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["all"] = AreaAt(0, 0).Span(1, 1).WidthFr(1).HeightFr(1)
	rects["all"] = Rect{X: 0, Y: 0, Width: 80, Height: 25}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutFractionalLeftRight(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["left"] = AreaAt(0, 0).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["right"] = AreaAt(1, 0).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["left"] = Rect{X: 0, Y: 0, Width: 40, Height: 25}
	rects["right"] = Rect{X: 40, Y: 0, Width: 40, Height: 25}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutFractionalCorners(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["topleft"] = AreaAt(0, 0).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["topright"] = AreaAt(1, 0).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["botleft"] = AreaAt(0, 1).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["botright"] = AreaAt(1, 1).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["topleft"] = Rect{X: 0, Y: 0, Width: 40, Height: 13}
	rects["topright"] = Rect{X: 40, Y: 0, Width: 40, Height: 13}
	rects["botleft"] = Rect{X: 0, Y: 13, Width: 40, Height: 12}
	rects["botright"] = Rect{X: 40, Y: 13, Width: 40, Height: 12}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutSidebarTwoColumn(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["sidebar"] = AreaAt(0, 0).Span(1, 1).WidthCh(10).HeightFr(1)
	areas["leftcol"] = AreaAt(1, 0).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["rightcol"] = AreaAt(2, 0).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["sidebar"] = Rect{X: 0, Y: 0, Width: 10, Height: 25}
	rects["leftcol"] = Rect{X: 10, Y: 0, Width: 35, Height: 25}
	rects["rightcol"] = Rect{X: 45, Y: 0, Width: 35, Height: 25}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutHeaderTwoColumn(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["header"] = AreaAt(0, 0).Span(2, 1).WidthFr(1).HeightCh(5)
	areas["leftcol"] = AreaAt(0, 1).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["rightcol"] = AreaAt(1, 1).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["header"] = Rect{X: 0, Y: 0, Width: 80, Height: 5}
	rects["leftcol"] = Rect{X: 0, Y: 5, Width: 40, Height: 20}
	rects["rightcol"] = Rect{X: 40, Y: 5, Width: 40, Height: 20}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutHeaderSidebarTwoColumn(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["header"] = AreaAt(0, 0).Span(3, 1).WidthFr(1).HeightCh(5)
	areas["sidebar"] = AreaAt(0, 1).Span(1, 2).WidthCh(10).HeightFr(1)
	areas["leftcol"] = AreaAt(1, 1).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["rightcol"] = AreaAt(2, 1).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["header"] = Rect{X: 0, Y: 0, Width: 80, Height: 5}
	rects["sidebar"] = Rect{X: 0, Y: 5, Width: 10, Height: 20}
	rects["leftcol"] = Rect{X: 10, Y: 5, Width: 35, Height: 20}
	rects["rightcol"] = Rect{X: 45, Y: 5, Width: 35, Height: 20}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutTwoRow(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["toprow"] = AreaAt(0, 0).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["botrow"] = AreaAt(0, 1).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["toprow"] = Rect{X: 0, Y: 0, Width: 80, Height: 13}
	rects["botrow"] = Rect{X: 0, Y: 13, Width: 80, Height: 12}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutHeaderTwoRow(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["header"] = AreaAt(0, 0).Span(1, 1).WidthFr(1).HeightCh(2)
	areas["toprow"] = AreaAt(0, 1).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["botrow"] = AreaAt(0, 2).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["header"] = Rect{X: 0, Y: 0, Width: 80, Height: 2}
	rects["toprow"] = Rect{X: 0, Y: 2, Width: 80, Height: 12}
	rects["botrow"] = Rect{X: 0, Y: 14, Width: 80, Height: 11}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutSidebarTwoRow(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["sidebar"] = AreaAt(0, 0).Span(1, 2).WidthCh(10).HeightFr(1)
	areas["toprow"] = AreaAt(1, 0).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["botrow"] = AreaAt(1, 1).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["sidebar"] = Rect{X: 0, Y: 0, Width: 10, Height: 25}
	rects["toprow"] = Rect{X: 10, Y: 0, Width: 70, Height: 13}
	rects["botrow"] = Rect{X: 10, Y: 13, Width: 70, Height: 12}

	checkGridLayout(t, 80, 25, areas, rects)
}

func TestGridLayoutHeaderSidebarTwoRow(t *testing.T) {
	areas := make(map[string]Area)
	rects := make(map[string]Rect)

	areas["header"] = AreaAt(0, 0).Span(2, 1).WidthFr(1).HeightCh(5)
	areas["sidebar"] = AreaAt(0, 1).Span(1, 2).WidthCh(10).HeightFr(1)
	areas["toprow"] = AreaAt(1, 1).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["botrow"] = AreaAt(1, 2).Span(1, 1).WidthFr(1).HeightFr(1)

	rects["header"] = Rect{X: 0, Y: 0, Width: 80, Height: 5}
	rects["sidebar"] = Rect{X: 0, Y: 5, Width: 10, Height: 20}
	rects["toprow"] = Rect{X: 10, Y: 5, Width: 70, Height: 10}
	rects["botrow"] = Rect{X: 10, Y: 15, Width: 70, Height: 10}

	checkGridLayout(t, 80, 25, areas, rects)
}
