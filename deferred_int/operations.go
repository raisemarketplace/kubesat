package deferred_int

// Constant creates a DeferredInt that always returns the value.
func Constant(a int) DeferredInt {
	return func() (int, bool) {
		return a, true
	}
}

// Max computes the maximum of two deferred ints, or if one is
// undefined, returns the other.
func Max(a, b DeferredInt) DeferredInt {
	return func() (int, bool) {
		va, ok := a()
		if !ok {
			return b()
		}

		vb, ok := b()
		if !ok {
			return va, true
		}

		if vb > va {
			return vb, true
		}
		return va, true
	}
}

// Difference computes the difference, a - b, and is undefined if
// either is undefined.
func Difference(a, b DeferredInt) DeferredInt {
	return func() (int, bool) {
		va, ok := a()
		if !ok {
			return 0, false
		}

		vb, ok := b()
		if !ok {
			return 0, false
		}

		return va - vb, true
	}
}

// SumAll computes the sum of all the deferred ints, and is undefined
// if any is undefined.
func SumAll(as []DeferredInt) DeferredInt {
	return func() (int, bool) {
		sum := 0
		for _, a := range as {
			va, ok := a()
			if !ok {
				return 0, false
			}
			sum += va
		}
		return sum, true
	}
}
