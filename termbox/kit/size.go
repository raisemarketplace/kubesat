package kit

// Size represents the size (length or width) of a screen area.
type Size interface {
	isSize()
}

// SizeCh is an absolute size in screen cells (characters).
type SizeCh int

func (s SizeCh) isSize() {}

// SizeFr is a fractional size, or weight. All fractional sizes in a
// given row or column are summed to be the denominator of the
// fraction, which the individual size is the numerator, minus any
// space already consumed by absolute sized areas.
type SizeFr int

func (s SizeFr) isSize() {}
