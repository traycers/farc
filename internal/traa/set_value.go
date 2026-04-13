package traa

func setValue(
	value int,
	target []int,
) {
	for i := range target {
		target[i] = value
	}
}
