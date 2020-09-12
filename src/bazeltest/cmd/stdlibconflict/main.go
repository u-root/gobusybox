package main

import (
	"log"

	// /math.a conflicts with /runtime/internal/math.a sometimes. Make sure
	// that doesn't happen.
	"math"
)

func main() {
	log.Printf("MaxInt64: %d", math.MaxInt32)
}
