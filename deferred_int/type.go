package deferred_int

// DeferredInt is an int that can be calculated later. This is
// represented by a function that returns an (int, true), or (int,
// false) to indicate the value is currently undefined.
type DeferredInt func() (size int, ok bool)
