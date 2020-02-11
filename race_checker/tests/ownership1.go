package main

func main() {
	k := 0
	readOnly(k)
	s := []int{1, 2, 3}
	readSlice(s)
}

func readOnly(i int) {
	m := i
	_ = m
	i = i + 1
}

func readSlice(s []int) {
	sum := 0
	for _, i := range s {
		sum += i
	}
}
