//go:build race

package ai

// raceOn lets the heavy single-goroutine search tests skip under -race: the
// detector cannot find anything in code without concurrency and only slows
// it tenfold. CI runs this package once more without -race to cover them.
const raceOn = true
