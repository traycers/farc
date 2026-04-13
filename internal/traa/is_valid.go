package traa

func IsValid[T any](
	t *Tree[T],
) bool {
	size := len(t.Parent)
	if size != len(t.Order) {
		return false
	}
	return true
}
