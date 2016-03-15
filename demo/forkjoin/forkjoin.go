// "Fork-join" parallelism, used in Java, can be expressed elegantly with
// async expressions. No "extends java.lang.Thread" or "public void run()" here!
//
// This demo runs a parallel fork-join sum algorithm and a sequential sum
// algorithm. Observe the fork-join speedup.
package main

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

const SequentialCutoff int = 1000
const Size int = 180000000

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println("Using", runtime.NumCPU(), "cpus")

	nums := make([]int, Size)
	for i := range nums {
		nums[i] = i
	}

	if len(os.Args) > 1 && os.Args[1] == "async" {
		fmt.Print("Running forkjoin sum... ")
		start := time.Now()
		SumAsync(nums, 0, len(nums))
		fmt.Println("finished in", time.Now().Sub(start))
	} else {
		fmt.Print("Running sequential sum... ")
		start := time.Now()
		Sum(nums, 0, len(nums))
		fmt.Println("finished in", time.Now().Sub(start))
	}

}

func SumAsync(nums []int, start, end int) int {
	if end-start < SequentialCutoff {
		return Sum(nums, start, end)
	} else {
		mid := (start + end) / 2
		left :=      (func() (func() int) {
	var __result0 int

	__ready := make(chan interface{})
	go func() {
		__result0 = Sum(nums, start, mid)
		close(__ready)
	}()
	return func() int {
			<-__ready
			return __result0
		}
}())
		right :=      (func() (func() int) {
	var __result0 int

	__ready := make(chan interface{})
	go func() {
		__result0 = Sum(nums, mid, end)
		close(__ready)
	}()
	return func() int {
			<-__ready
			return __result0
		}
}())
		return left() + right()
	}
}

func Sum(nums []int, start, end int) int {
	sum := 0
	for _, num := range nums[start:end] {
		sum += num
	}
	return sum
}
