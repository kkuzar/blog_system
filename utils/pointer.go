package pointer

// To returns a pointer to the given value.
// Useful for creating pointers to primitive literals (e.g., pointer.To(5)).
func To[T any](v T) *T {
	return &v
}
