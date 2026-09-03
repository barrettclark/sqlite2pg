// Package heuristics holds the type-inference heuristics, one per file.
// Each implements profiler.Heuristic and self-registers via init(), so
// adding a case never means touching an existing one.
package heuristics
