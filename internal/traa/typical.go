package traa

func Size[T any](t *Tree[T]) int {
	return len(t.Parent)
}

func Empty[T any](t *Tree[T]) bool {
	return Size(t) == 0
}
