package main

import (
	"flag"
	"fmt"
	"math/rand"
)

const numBits = 256
const digits = "0123456789abcdef"

var seed int64

func main() {
	flag.Int64Var(&seed, "seed", 0xdeadc0de, "seed")
	flag.Parse()

	rand.Seed(seed)

	numDigits := numBits / 4
	digitsArray := []byte(digits)

	key := make([]byte, numDigits)

	for i := 0; i < numDigits; i++ {
		key[i] = digitsArray[rand.Intn(len(digitsArray))]
	}

	fmt.Printf("%s", string(key))
}
