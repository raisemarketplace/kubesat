package kit

import (
	"fmt"

	termbox "github.com/nsf/termbox-go"
)

// BufferSlice is a sub-rectangle within a larger Buffer.
type BufferSlice struct {
	x      int
	y      int
	Width  int
	Height int
	Stride int
	cells  []termbox.Cell
}

func TermboxBackBuffer() BufferSlice {
	width, height := termbox.Size()
	return BufferSlice{x: 0, y: 0, Width: width, Height: height, Stride: width, cells: termbox.CellBuffer()}
}

func min(a, b int) int {
	if a > b {
		return b
	}
	return a
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (sl BufferSlice) Slice(x, y, width, height int) BufferSlice {
	if x < 0 {
		panic(fmt.Sprintf("x must be non-negative: %d", x))
	}
	if y < 0 {
		panic(fmt.Sprintf("y must be non-negative: %d", x))
	}
	if width < 0 {
		panic(fmt.Sprintf("width must be non-negative:: %d", width))
	}
	if height < 0 {
		panic(fmt.Sprintf("height must be non-negative > 0: %d", height))
	}

	x = min(x+sl.x, sl.x+sl.Width)
	if x+width > sl.x+sl.Width {
		width = sl.x + sl.Width - x
	}

	y = min(y+sl.y, sl.y+sl.Height)
	if y+height > sl.y+sl.Height {
		height = sl.y + sl.Height - y
	}

	return BufferSlice{
		x:      x,
		y:      y,
		Width:  width,
		Height: height,
		Stride: sl.Stride,
		cells:  sl.cells}
}

// Get returns a pointer to the underlying Cell in the backing buffer,
// or nil if out of bounds.
func (sl BufferSlice) Get(x, y int) (*termbox.Cell, bool) {
	if x < 0 || x >= sl.Width || y < 0 || y >= sl.Height {
		return nil, false
	}

	return &sl.cells[sl.Stride*(sl.y+y)+sl.x+x], true
}

func (sl BufferSlice) SetFg(a termbox.Attribute) {
	for y := 0; y < sl.Height; y++ {
		for x := 0; x < sl.Width; x++ {
			if cell, ok := sl.Get(x, y); ok {
				cell.Fg = a
			}
		}
	}
}

func (sl BufferSlice) SetBg(a termbox.Attribute) {
	for y := 0; y < sl.Height; y++ {
		for x := 0; x < sl.Width; x++ {
			if cell, ok := sl.Get(x, y); ok {
				cell.Bg = a
			}
		}
	}
}
