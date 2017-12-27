package kit

import (
	"unicode/utf8"

	termbox "github.com/nsf/termbox-go"
	"golang.org/x/text/unicode/norm"
)

type TableCell interface {
	Draw(dest BufferSlice)
	Width() int
}

type Rune rune

func (ch Rune) Draw(dest BufferSlice) {
	cell, ok := dest.Get(0, 0)
	if !ok {
		return
	}
	cell.Ch = rune(ch)
}

func (ch Rune) Width() int {
	return 1
}

type String string

func (s String) Draw(dest BufferSlice) {
	var iter norm.Iter
	iter.InitString(norm.NFC, string(s))

	i := 0
	for !iter.Done() {
		for _, ch := range string(iter.Next()) {
			if cell, ok := dest.Get(i, 0); ok {
				cell.Ch = ch
			}
			i++
		}
	}
}

// Width calculates the width of the string after normalizing to
// Canonical Composition form, with the assumption that each rune in
// that form corrresponds to a single cell on screen.
func (s String) Width() int {
	var iter norm.Iter
	iter.InitString(norm.NFC, string(s))

	count := 0
	for !iter.Done() {
		count = count + utf8.RuneCount(iter.Next())
	}
	return count
}

type AttrString struct {
	Value string
	Fg    termbox.Attribute
	Bg    termbox.Attribute
}

func (a AttrString) Width() int {
	return String(a.Value).Width()
}

func (a AttrString) Draw(dest BufferSlice) {
	String(a.Value).Draw(dest)
	for i := 0; i < a.Width(); i++ {
		if cell, ok := dest.Get(i, 0); ok {
			if a.Fg != 0 {
				cell.Fg = a.Fg
			}
			if a.Bg != 0 {
				cell.Bg = a.Bg
			}
		}
	}

}

type Cell termbox.Cell

func (c Cell) Draw(dest BufferSlice) {
	cell, ok := dest.Get(0, 0)
	if !ok {
		return
	}
	cell.Ch = c.Ch
	if c.Fg != 0 {
		cell.Fg = c.Fg
	}
	if c.Bg != 0 {
		cell.Bg = c.Bg
	}
}

func (c Cell) Width() int {
	return 1
}

type CellString []termbox.Cell

func (cs CellString) Draw(dest BufferSlice) {
	for i, c := range cs {
		if cell, ok := dest.Get(i, 0); ok {
			*cell = c
		}
	}
}

func (cs CellString) Width() int {
	return len(cs)
}

// Line is a horizontal list of TableCell
type Line []TableCell

func (line Line) Width() int {
	i := 0
	for _, cell := range line {
		i = i + cell.Width()
	}
	return i
}

func (line Line) Draw(buf BufferSlice) {
	pos := 0
	for _, cell := range line {
		width := cell.Width()
		cell.Draw(buf.Slice(pos, 0, width, 1))
		pos = pos + width
	}
}

type TableRow struct {
	Cells []TableCell
	Fg    termbox.Attribute
	Bg    termbox.Attribute
}

func RowWithAttributes(fg, bg termbox.Attribute, cells ...TableCell) TableRow {
	return TableRow{Cells: cells, Fg: fg, Bg: bg}
}

func Row(cells ...TableCell) TableRow {
	return TableRow{Cells: cells}
}

type Table struct {
	Rows []TableRow
}

func (table *Table) ColumnCount() int {
	count := 0
	for _, row := range table.Rows {
		if count < len(row.Cells) {
			count = len(row.Cells)
		}
	}
	return count
}

func (table *Table) ColumnWidths() []int {
	widths := make([]int, table.ColumnCount(), table.ColumnCount())
	for _, row := range table.Rows {
		for x, tableCell := range row.Cells {
			w := tableCell.Width()
			if widths[x] < w {
				widths[x] = w
			}
		}
	}
	return widths
}

func (table *Table) Draw(dest BufferSlice) {
	widths := table.ColumnWidths()

	for y, row := range table.Rows {
		if row.Fg != 0 {
			dest.Slice(0, y, dest.Width, 1).SetFg(row.Fg)
		}
		if row.Bg != 0 {
			dest.Slice(0, y, dest.Width, 1).SetBg(row.Bg)
		}

		xoffset := 0
		for x, tableCell := range row.Cells {
			width := widths[x]
			tableCell.Draw(dest.Slice(xoffset, y, width, 1))
			xoffset = xoffset + width
		}
	}
}
