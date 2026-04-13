package traa

func eq(
	a []int,
	b []int,
) bool {
	size := len(a)
	if size != len(b) {
		return false
	}
	for i := range size {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
