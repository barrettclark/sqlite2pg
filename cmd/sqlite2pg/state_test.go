package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLoadState_EmptyWhenFileDoesNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.state.json")
	completed, err := loadCompletedTables(path)
	if err != nil {
		t.Fatalf("loadCompletedTables: %v", err)
	}
	if len(completed) != 0 {
		t.Errorf("expected no completed tables, got %v", completed)
	}
}

func TestMarkTableCompleted_PersistsAndAccumulates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.state.json")

	if err := writeState(path, loadState{Database: "chinook_20260830_120000"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := markTableCompleted(path, "bikes"); err != nil {
		t.Fatalf("markTableCompleted: %v", err)
	}
	if err := markTableCompleted(path, "construction"); err != nil {
		t.Fatalf("markTableCompleted: %v", err)
	}

	completed, err := loadCompletedTables(path)
	if err != nil {
		t.Fatalf("loadCompletedTables: %v", err)
	}
	if !completed["bikes"] || !completed["construction"] {
		t.Errorf("expected both tables marked completed, got %v", completed)
	}
}

// TestMarkTableCompleted_PreservesRecordedDatabase is the regression test
// for issue #19: once a run has recorded which database it provisioned,
// every subsequent write to the state file (as each table finishes) must
// keep that database name intact rather than losing it. This is what lets
// --resume reconnect to the same database instead of provisioning a new,
// empty one.
func TestMarkTableCompleted_PreservesRecordedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.state.json")

	if err := writeState(path, loadState{Database: "chinook_20260830_120000"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := markTableCompleted(path, "albums"); err != nil {
		t.Fatalf("markTableCompleted: %v", err)
	}
	if err := markTableCompleted(path, "artists"); err != nil {
		t.Fatalf("markTableCompleted: %v", err)
	}

	st, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if st.Database != "chinook_20260830_120000" {
		t.Errorf("expected recorded database to survive markTableCompleted, got %q", st.Database)
	}
	if len(st.Completed) != 2 {
		t.Errorf("expected 2 completed tables, got %v", st.Completed)
	}
}

// TestReadState_EmptyWhenFileDoesNotExist mirrors
// TestLoadState_EmptyWhenFileDoesNotExist but through the new readState
// entry point: a fresh --resume invocation with no prior state must not
// treat a missing file as "reconnect to database \"\"".
func TestReadState_EmptyWhenFileDoesNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.state.json")
	st, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if st.Database != "" || len(st.Completed) != 0 {
		t.Errorf("expected zero-value state, got %+v", st)
	}
}

// TestMarkForeignKeysApplied_PersistsAndPreservesDatabaseAndCompleted
// mirrors TestMarkTableCompleted_PreservesRecordedDatabase: recording that
// the FK step finished must not clobber the database name or the
// completed-tables list already on file.
func TestMarkForeignKeysApplied_PersistsAndPreservesDatabaseAndCompleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.state.json")

	if err := writeState(path, loadState{Database: "chinook_20260830_120000", Completed: []string{"albums"}}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := markForeignKeysApplied(path); err != nil {
		t.Fatalf("markForeignKeysApplied: %v", err)
	}

	st, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if !st.FKsApplied {
		t.Error("expected FKsApplied to be true")
	}
	if st.Database != "chinook_20260830_120000" {
		t.Errorf("expected recorded database to survive markForeignKeysApplied, got %q", st.Database)
	}
	if len(st.Completed) != 1 || st.Completed[0] != "albums" {
		t.Errorf("expected completed-tables list to survive markForeignKeysApplied, got %v", st.Completed)
	}
}

// stateStage injects one failure point in writeState.
type stateStage struct {
	name   string
	inject func(t *testing.T, injected error)
}

// injectTempFault wraps the real temp file so the chosen operation fails.
func injectTempFault(t *testing.T, fault faultTempFile) {
	t.Helper()
	orig := openStateTemp
	t.Cleanup(func() { openStateTemp = orig })
	openStateTemp = func(name string) (stateTempFile, error) {
		f, err := orig(name)
		if err != nil {
			return nil, err
		}
		fault.stateTempFile = f
		return fault, nil
	}
}

// faultTempFile fails the named operation after delegating to the real file.
type faultTempFile struct {
	stateTempFile
	writeErr, syncErr, closeErr error
}

func (f faultTempFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.stateTempFile.Write(p)
}

func (f faultTempFile) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.stateTempFile.Sync()
}

func (f faultTempFile) Close() error {
	err := f.stateTempFile.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return err
}

// TestWriteState_FailureKeepsPreviousState injects a failure at each step of
// writeState. The previous state must stay readable and no temp file may be
// left behind.
func TestWriteState_FailureKeepsPreviousState(t *testing.T) {
	injected := errors.New("injected failure")
	stages := []stateStage{
		{
			name: "create temp",
			inject: func(t *testing.T, err error) {
				orig := openStateTemp
				t.Cleanup(func() { openStateTemp = orig })
				openStateTemp = func(string) (stateTempFile, error) { return nil, err }
			},
		},
		{
			name: "write",
			inject: func(t *testing.T, err error) {
				injectTempFault(t, faultTempFile{writeErr: err})
			},
		},
		{
			name: "sync",
			inject: func(t *testing.T, err error) {
				injectTempFault(t, faultTempFile{syncErr: err})
			},
		},
		{
			name: "close",
			inject: func(t *testing.T, err error) {
				injectTempFault(t, faultTempFile{closeErr: err})
			},
		},
		{
			name: "rename",
			inject: func(t *testing.T, err error) {
				orig := renameFile
				t.Cleanup(func() { renameFile = orig })
				renameFile = func(string, string) error { return err }
			},
		},
	}
	prevs := []struct {
		name string
		prev *loadState
		next loadState
		want loadState
	}{
		{
			name: "replaces existing state",
			prev: &loadState{Database: "chinook_a", Completed: []string{"albums"}},
			next: loadState{Database: "chinook_a", Completed: []string{"albums", "artists"}},
			want: loadState{Database: "chinook_a", Completed: []string{"albums"}},
		},
		{
			name: "no prior state",
			next: loadState{Database: "chinook_b"},
			want: loadState{},
		},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			for _, tt := range prevs {
				t.Run(tt.name, func(t *testing.T) {
					dir := t.TempDir()
					path := filepath.Join(dir, "run.state.json")
					if tt.prev != nil {
						if err := writeState(path, *tt.prev); err != nil {
							t.Fatalf("writeState: %v", err)
						}
					}

					stage.inject(t, injected)

					err := writeState(path, tt.next)
					if !errors.Is(err, injected) {
						t.Fatalf("writeState error = %v, want %v", err, injected)
					}

					got, err := readState(path)
					if err != nil {
						t.Fatalf("readState after failed write: %v", err)
					}
					if got.Database != tt.want.Database || fmt.Sprint(got.Completed) != fmt.Sprint(tt.want.Completed) {
						t.Errorf("state after failed write = %+v, want %+v", got, tt.want)
					}

					entries, err := os.ReadDir(dir)
					if err != nil {
						t.Fatalf("ReadDir: %v", err)
					}
					for _, e := range entries {
						if e.Name() != "run.state.json" {
							t.Errorf("leftover file after failed write: %s", e.Name())
						}
					}
				})
			}
		})
	}
}

// overlapBarrier releases callers once n have arrived. The timeout keeps a
// locked implementation from deadlocking: its callers arrive one at a time.
type overlapBarrier struct {
	mu      sync.Mutex
	arrived int
	n       int
	release chan struct{}
}

func newOverlapBarrier(n int) *overlapBarrier {
	return &overlapBarrier{n: n, release: make(chan struct{})}
}

func (b *overlapBarrier) wait() {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.release)
	}
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-time.After(250 * time.Millisecond):
	}
}

// TestMarkTableCompleted_OverlappingReadsKeepAllEntries forces every caller to
// read the same snapshot before any caller writes. Without stateMu, the last
// write wins and drops the other entries.
func TestMarkTableCompleted_OverlappingReadsKeepAllEntries(t *testing.T) {
	tests := []struct {
		name   string
		tables []string
	}{
		{name: "two writers", tables: []string{"albums", "artists"}},
		{name: "three writers", tables: []string{"albums", "artists", "tracks"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.state.json")
			if err := writeState(path, loadState{Database: "chinook_o"}); err != nil {
				t.Fatalf("writeState: %v", err)
			}

			bar := newOverlapBarrier(len(tt.tables))
			orig := afterStateRead
			t.Cleanup(func() { afterStateRead = orig })
			afterStateRead = bar.wait

			var wg sync.WaitGroup
			errs := make(chan error, len(tt.tables))
			for _, table := range tt.tables {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs <- markTableCompleted(path, table)
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatalf("markTableCompleted: %v", err)
				}
			}

			st, err := readState(path)
			if err != nil {
				t.Fatalf("readState: %v", err)
			}
			if len(st.Completed) != len(tt.tables) {
				t.Errorf("completed = %v, want %d entries (lost update)", st.Completed, len(tt.tables))
			}
		})
	}
}

// TestMarkTableCompleted_ConcurrentCallsKeepAllEntries checks that
// simultaneous markTableCompleted calls do not lose each other's updates.
func TestMarkTableCompleted_ConcurrentCallsKeepAllEntries(t *testing.T) {
	tests := []struct {
		name    string
		workers int
		rounds  int
	}{
		{name: "two writers", workers: 2, rounds: 50},
		{name: "many writers", workers: 16, rounds: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for round := range tt.rounds {
				path := filepath.Join(t.TempDir(), "run.state.json")
				if err := writeState(path, loadState{Database: "chinook_c"}); err != nil {
					t.Fatalf("writeState: %v", err)
				}

				start := make(chan struct{})
				var wg sync.WaitGroup
				errs := make(chan error, tt.workers)
				for i := range tt.workers {
					wg.Add(1)
					go func(table string) {
						defer wg.Done()
						<-start
						errs <- markTableCompleted(path, table)
					}(fmt.Sprintf("table_%d", i))
				}
				close(start)
				wg.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						t.Fatalf("round %d: markTableCompleted: %v", round, err)
					}
				}

				st, err := readState(path)
				if err != nil {
					t.Fatalf("round %d: readState: %v", round, err)
				}
				if st.Database != "chinook_c" {
					t.Errorf("round %d: database = %q, want chinook_c", round, st.Database)
				}
				if len(st.Completed) != tt.workers {
					t.Errorf("round %d: completed = %d entries, want %d (lost update): %v",
						round, len(st.Completed), tt.workers, st.Completed)
				}
			}
		})
	}
}
