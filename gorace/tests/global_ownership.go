package gorace_test

var j int

func main() {
	go func() {
		_ = j /* RACE Read */
	}()
	j /* RACE Write */ = 1
}
