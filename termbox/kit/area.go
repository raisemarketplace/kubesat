package kit

type Area struct {
	Col  int
	Row  int
	Cols int
	Rows int

	Width  Size
	Height Size
}

func AreaAt(x, y int) Area {
	return Area{Col: x, Row: y}
}

func (a Area) Span(cols, rows int) Area {
	a.Cols = cols
	a.Rows = rows
	return a
}

func (a Area) WidthCh(n int) Area {
	a.Width = SizeCh(n)
	return a
}

func (a Area) WidthFr(n int) Area {
	a.Width = SizeFr(n)
	return a
}

func (a Area) HeightCh(n int) Area {
	a.Height = SizeCh(n)
	return a
}

func (a Area) HeightFr(n int) Area {
	a.Height = SizeFr(n)
	return a
}

func (a Area) EachCol(fn func(col int)) {
	for i := a.Col; i < a.Col+a.Cols; i++ {
		fn(i)
	}
}

func (a Area) EachRow(fn func(row int)) {
	for i := a.Row; i < a.Row+a.Rows; i++ {
		fn(i)
	}
}

func (a Area) MaxCol() int {
	return a.Col + a.Cols
}

func (a Area) MaxRow() int {
	return a.Row + a.Rows
}
