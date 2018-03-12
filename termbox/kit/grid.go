package kit

import (
	"fmt"

	di "github.com/raisemarketplace/kubesat/deferred_int"
)

type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

func (r Rect) String() string {
	return fmt.Sprintf("x=%d,y=%d;w=%d,h=%d", r.X, r.Y, r.Width, r.Height)
}

type Grid struct {
	Items map[string]Drawer
	areas map[string]Area
}

type stringwrap struct{ String string }

func max(a, b int) int {
	if b > a {
		return b
	}
	return a
}

func NewGrid(areas map[string]Area) *Grid {
	grid := &Grid{
		Items: make(map[string]Drawer),
		areas: areas,
	}

	// ensure no overlaps
	height, width := grid.RowsAndColumns()

	points := make([]*stringwrap, width*height, width*height)
	for name, area := range areas {
		area.EachCol(func(col int) {
			area.EachRow(func(row int) {
				name2 := points[row*width+col]
				if name2 != nil {
					panic(fmt.Sprintf("area '%s' overlaps area '%s' at %d,%d", name, name2.String, col, row))
				}
				points[row*width+col] = &stringwrap{String: name}
			})
		})
	}

	return grid
}

func (grid *Grid) Draw(dest BufferSlice) {
	// TODO: maybe cache layout result
	bufs := grid.LayoutBuffers(dest)

	for name, drawer := range grid.Items {
		if drawer == nil {
			continue
		}
		drawer.Draw(bufs[name])
	}
}

func (grid *Grid) RowsAndColumns() (int, int) {
	rows := 0
	cols := 0
	for _, area := range grid.areas {
		rows = max(rows, area.MaxRow())
		cols = max(cols, area.MaxCol())
	}
	return rows, cols
}

func sumExcept(all *DeferredSizes, skip int) di.DeferredInt {
	return func() (int, bool) {
		sum := 0
		for i := 0; i < all.Length(); i++ {
			if i == skip {
				continue
			}

			size, ok := all.Calculate(i)
			if !ok {
				return 0, false
			}
			sum += size
		}
		return sum, true
	}
}

func sumAtIndexes(all *DeferredSizes, indexes []int) di.DeferredInt {
	return func() (int, bool) {
		sum := 0
		for _, i := range indexes {
			size, ok := all.Calculate(i)
			if !ok {
				return 0, false
			}
			sum += size
		}
		return sum, true
	}
}

func sequence(from, limit int) []int {
	s := make([]int, 0, limit)
	for i := from; i < limit; i++ {
		s = append(s, i)
	}
	return s
}

func skip(a []int, toSkip int) []int {
	s := make([]int, 0, max(0, len(a)-1))
	for _, v := range a {
		if v == toSkip {
			continue
		}
		s = append(s, v)
	}
	return s
}

func remainder(totalSize int, all *DeferredSizes, self int) di.DeferredInt {
	return di.Difference(di.Constant(totalSize), sumExcept(all, self))
}

func fractional(totalSize, numerator int, sizes *SizesGroup) di.DeferredInt {
	return func() (int, bool) {
		denominator := 0

		for _, size := range sizes.Sizes {
			switch value := size.(type) {
			case SizeFr:
				denominator += int(value)
			case SizeCh:
				totalSize -= int(value)
			}
		}

		return int(float64(numerator) / float64(denominator) * float64(totalSize)), true
	}
}

type SizesGroup struct {
	Sizes []Size
}

type DeferredSizes struct {
	sizes []di.DeferredInt

	pending    map[int]bool
	calculated map[int]int
}

func NewDeferredSizes(length int) *DeferredSizes {
	d := &DeferredSizes{
		sizes:      make([]di.DeferredInt, length, length),
		pending:    make(map[int]bool),
		calculated: make(map[int]int),
	}

	undefined := func() (int, bool) {
		return 0, false
	}

	for i, _ := range d.sizes {
		d.sizes[i] = undefined
	}

	return d
}

func (d *DeferredSizes) Length() int {
	return len(d.sizes)
}

func (d *DeferredSizes) Calculate(i int) (size int, ok bool) {
	isPending, ok := d.pending[i]
	if isPending && ok {
		return 0, false
	}

	cached, ok := d.calculated[i]
	if ok {
		return cached, true
	}

	fn := d.sizes[i]

	d.pending[i] = true
	defer func() { delete(d.pending, i) }()

	value, ok := fn()
	if ok {
		d.calculated[i] = value
	}

	return value, ok
}

func (d *DeferredSizes) CalculateAll() []int {
	sizes := make([]int, len(d.sizes), len(d.sizes))

	for i, _ := range d.sizes {
		size, ok := d.Calculate(i)
		if !ok {
			// log a warning?
			size = 0
		}
		sizes[i] = size
	}

	return sizes
}

func (d *DeferredSizes) Push(i int, fn di.DeferredInt) {
	d.sizes[i] = di.Max(d.sizes[i], fn)
}

// Layout the grid into a rectangle of the given dimensions. The units
// of the returned rectangles are in cells.
func (grid *Grid) Layout(width, height int) map[string]Rect {
	nrows, ncols := grid.RowsAndColumns()

	// rows have variable height
	rowHeights := NewDeferredSizes(nrows)

	// cols have variable width
	colWidths := NewDeferredSizes(ncols)

	// for each col, all areas that include a given row
	rowSizes := make([]*SizesGroup, ncols, ncols)
	for col, _ := range rowSizes {
		rowSizes[col] = &SizesGroup{Sizes: make([]Size, 0, 0)}
	}

	// for each row, all areas that include a given col
	colSizes := make([]*SizesGroup, nrows, nrows)
	for row, _ := range colSizes {
		colSizes[row] = &SizesGroup{Sizes: make([]Size, 0, 0)}
	}

	for _, area := range grid.areas {
		// for every row in this area, append area width to col widths for row
		area.EachRow(func(row int) {
			colSizes[row].Sizes = append(colSizes[row].Sizes, area.Width)
		})

		// for every col in this area, append area height to row heights for col
		area.EachCol(func(col int) {
			rowSizes[col].Sizes = append(rowSizes[col].Sizes, area.Height)
		})

		switch size := area.Width.(type) {
		case SizeCh:
			if area.Cols == 1 {
				colWidths.Push(area.Col, di.Constant(int(size)))
			} else {
				// each col width is the constant size minus the width of all the others
				area.EachCol(func(col int) {
					others := skip(sequence(area.Col, area.Cols), col)
					colWidths.Push(col, di.Difference(di.Constant(int(size)), sumAtIndexes(colWidths, others)))
				})
			}
		case SizeFr:
			// for every row this area touches
			area.EachRow(func(row int) {
				total := fractional(width, int(size), colSizes[row])
				colSpan := sequence(area.Col, area.Col+area.Cols)

				// for every col
				area.EachCol(func(col int) {
					others := skip(colSpan, col)
					colWidths.Push(col, di.Difference(total, sumAtIndexes(colWidths, others)))
					colWidths.Push(col, remainder(width, colWidths, col))
				})
			})
		}

		switch size := area.Height.(type) {
		case SizeCh:
			if area.Rows == 1 {
				rowHeights.Push(area.Row, di.Constant(int(size)))
			} else {
				// each row height is the constant size minus the height of all the others
				area.EachRow(func(row int) {
					others := skip(sequence(area.Row, area.Rows), row)
					rowHeights.Push(row, di.Difference(di.Constant(int(size)), sumAtIndexes(rowHeights, others)))
				})
			}
		case SizeFr:
			// for every col this area touches
			area.EachCol(func(col int) {
				total := fractional(height, int(size), rowSizes[col])
				rowSpan := sequence(area.Row, area.Row+area.Rows)

				// for every row
				area.EachRow(func(row int) {
					others := skip(rowSpan, row)
					rowHeights.Push(row, di.Difference(total, sumAtIndexes(rowHeights, others)))
					rowHeights.Push(row, remainder(height, rowHeights, row))

				})
			})
		}
	}

	// determine the grid size in char cells
	rowChars := rowHeights.CalculateAll()
	colChars := colWidths.CalculateAll()

	rects := make(map[string]Rect)
	for name, area := range grid.areas {
		rect := Rect{}

		for x := 0; x < area.Col; x++ {
			rect.X += colChars[x]
		}
		area.EachCol(func(x int) {
			rect.Width += colChars[x]
		})

		for y := 0; y < area.Row; y++ {
			rect.Y += rowChars[y]
		}
		area.EachRow(func(y int) {
			rect.Height += rowChars[y]
		})

		rects[name] = rect
	}

	return rects
}

func (grid *Grid) LayoutBuffers(buf BufferSlice) map[string]BufferSlice {
	bufs := make(map[string]BufferSlice)

	for name, rect := range grid.Layout(buf.Width, buf.Height) {
		bufs[name] = buf.Slice(rect.X, rect.Y, rect.Width, rect.Height)
	}

	return bufs
}
