package traa

func setValueByIndex(
	value int,
	idx []int,
	target []int,
) {
	for _, it := range idx {
		target[it] = value
	}
}
