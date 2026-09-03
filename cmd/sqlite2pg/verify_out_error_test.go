package main

import (
	"errors"
	"testing"

	"sqlite2pg/internal/pipeline"
)

type failingWriter struct{ afterN int }

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.afterN <= 0 {
		return 0, errors.New("disk full")
	}
	n := len(p)
	if n > f.afterN {
		n = f.afterN
	}
	f.afterN -= n
	return n, nil
}

// TestErrWriter_LatchesFirstWriteError is issue #136: writeVerifyReport
// does dozens of unchecked fmt.Fprint* calls, so an I/O failure part-way
// through the --out file has to be caught by the wrapping writer or the
// report is silently truncated with a zero exit.
func TestErrWriter_LatchesFirstWriteError(t *testing.T) {
	ew := &errWriter{w: &failingWriter{afterN: 40}}
	results := []pipeline.TableVerifyResult{
		{Table: "widgets", SourceRowCount: 3, TargetRowCount: 3, RowsCompared: 3, ColumnResults: map[string]*pipeline.ColumnVerifyResult{}},
	}

	writeVerifyReport(ew, results, nil)

	if ew.err == nil {
		t.Fatal("errWriter did not latch the write failure; runVerify would report a truncated --out file as success (#136)")
	}
	// Every subsequent Write returns the latched error, not a fresh one.
	if _, err := ew.Write([]byte("more")); err != ew.err {
		t.Errorf("Write after a failure returned %v, want the latched %v", err, ew.err)
	}
}

// TestErrWriter_CleanWritePassesThrough guards against the wrapper
// swallowing a successful run.
func TestErrWriter_CleanWritePassesThrough(t *testing.T) {
	var sink discardWriter
	ew := &errWriter{w: &sink}
	writeVerifyReport(ew, nil, []string{"geometry_only"})
	if ew.err != nil {
		t.Errorf("errWriter latched an error on a clean write: %v", ew.err)
	}
	if sink.n == 0 {
		t.Error("expected the report to reach the underlying writer")
	}
}

type discardWriter struct{ n int }

func (d *discardWriter) Write(p []byte) (int, error) { d.n += len(p); return len(p), nil }
