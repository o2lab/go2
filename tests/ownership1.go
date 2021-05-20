package main

func main() {
	k := 0
	readOnly(k)
	s := []int{1, 2, 3}
	readSlice(s)
}

func readOnly(i int) {
	m3 := i
	_ = m3
	i = i + 1
}

func readSlice(s []int) {
	sum := 0
	for _, i3 := range s {
		sum += i3
	}
}
