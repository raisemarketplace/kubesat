package kit

import (
	termbox "github.com/nsf/termbox-go"
)

func Update(fg, bg termbox.Attribute, update func(BufferSlice)) {
	termbox.Clear(fg, bg)
	width, height := termbox.Size()
	buf := TermboxBackBuffer().Slice(0, 0, width, height)
	update(buf)
	termbox.Flush()
}
