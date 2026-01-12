package main

import (
	"fmt"
	"strconv"
)

func main() {
	fmt.Println("खा")
	fmt.Println(strconv.QuoteToASCII("खा"))
	fmt.Println("\u0916 \u093e")
	fmt.Println("\u0916\u093e")
}
