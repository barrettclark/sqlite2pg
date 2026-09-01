package ddl

import (
	"strings"
	"testing"
)

// FuzzDisambiguateNamesInvariants checks the three properties every caller
// of disambiguateNames (PostgresTableNames, PostgresColumnNames,
// foreignKeyIndexNames, foreignKeyConstraintNames) silently relies on.
// This is the code path this project has patched three separate times for
// the same "two similar strings collide after truncation" shape (issues
// #21, #43, #44):
//
//  1. every output is <= 63 bytes (Postgres NAMEDATALEN — an output over
//     the limit gets truncated again by the server, re-colliding);
//  2. distinct identities never map to a shared output (that's the whole
//     job — a collision here is "relation already exists" / "column
//     specified more than once");
//  3. the mapping is a pure function of each (display, identity) pair:
//     reordering the input, or splitting it across calls, must not change
//     any entry's output (the doc comment promises stability across
//     separate profile/review/load/verify runs).
func FuzzDisambiguateNamesInvariants(f *testing.F) {
	f.Add("a\x00b\x00c", "a\x00b\x00c")
	// two names identical in their first 63 bytes, differing after
	long := strings.Repeat("x", 70)
	f.Add(long+"1\x00"+long+"2", long+"1\x00"+long+"2")
	// a short name that could clash with a disambiguated long one
	f.Add(strings.Repeat("y", 63)+"A\x00"+strings.Repeat("y", 63)+"B\x00"+strings.Repeat("y", 54), "")
	f.Add("dup\x00dup", "id1\x00id2")

	f.Fuzz(func(t *testing.T, displayJoined, identityJoined string) {
		display := strings.Split(displayJoined, "\x00")
		var identity []string
		if identityJoined == "" {
			identity = append([]string(nil), display...) // identity == display, the common case
		} else {
			identity = strings.Split(identityJoined, "\x00")
			if len(identity) != len(display) {
				return // malformed fuzz input, not a case we define
			}
		}
		if len(display) > 64 {
			return // keep iterations cheap
		}

		out := disambiguateNames(display, identity)
		if len(out) != len(display) {
			t.Fatalf("disambiguateNames returned %d names for %d inputs", len(out), len(display))
		}

		// (1) length limit
		for i, o := range out {
			if len(o) > maxIdentifierLen {
				t.Fatalf("output %q for display=%q identity=%q is %d bytes > %d",
					o, display[i], identity[i], len(o), maxIdentifierLen)
			}
		}

		// (2) distinct identities => distinct outputs
		byOutput := map[string]int{}
		for i, o := range out {
			if j, seen := byOutput[o]; seen && identity[i] != identity[j] {
				t.Fatalf("collision: identity %q (#%d) and identity %q (#%d) both map to output %q\n"+
					"display: %q / %q",
					identity[i], i, identity[j], j, o, display[i], display[j])
			}
			byOutput[o] = i
		}

		// (3) purity / order-independence: reverse the input, the
		// per-entry mapping must be unchanged.
		rd := reversed(display)
		ri := reversed(identity)
		rout := disambiguateNames(rd, ri)
		for i := range display {
			back := rout[len(rout)-1-i]
			if back != out[i] {
				t.Fatalf("not order-independent: entry %d (display=%q identity=%q) mapped to %q normally but %q when input reversed",
					i, display[i], identity[i], out[i], back)
			}
		}
	})
}

func reversed(s []string) []string {
	r := make([]string, len(s))
	for i, v := range s {
		r[len(s)-1-i] = v
	}
	return r
}

// FuzzQuoteIdentRoundTrips checks that quoteIdent produces a well-formed
// double-quoted SQL identifier that decodes back to the original name for
// any input — issue #26 was a divergence between DDL identifier quoting
// (then fmt.Sprintf("%q", ...)) and the COPY path's pgx.Identifier.Sanitize.
// The output must start and end with a double quote, and un-doubling the
// interior quotes must reproduce the input exactly.
func FuzzQuoteIdentRoundTrips(f *testing.F) {
	f.Add("plain")
	f.Add(`has"quote`)
	f.Add(`ends"`)
	f.Add(`"`)
	f.Add(`a"b"c`)
	f.Add("back\\slash")
	f.Add("newline\nhere")
	f.Add("")
	f.Add("unicode_🙂_name")

	f.Fuzz(func(t *testing.T, name string) {
		// pgx.Identifier.Sanitize (which quoteIdent delegates to)
		// deliberately strips NUL bytes; DDL and COPY both go through it,
		// so they still agree — the round-trip identity just isn't
		// defined for NUL-containing names.
		if strings.ContainsRune(name, 0) {
			return
		}

		q := quoteIdent(name)

		if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
			t.Fatalf("quoteIdent(%q) = %q — not wrapped in double quotes", name, q)
		}
		inner := q[1 : len(q)-1]

		// Every '"' in the interior must be part of a doubled "" pair.
		var decoded strings.Builder
		for i := 0; i < len(inner); i++ {
			if inner[i] != '"' {
				decoded.WriteByte(inner[i])
				continue
			}
			if i+1 >= len(inner) || inner[i+1] != '"' {
				t.Fatalf("quoteIdent(%q) = %q — lone '\"' at interior offset %d, not SQL-doubled", name, q, i)
			}
			decoded.WriteByte('"')
			i++
		}
		if decoded.String() != name {
			t.Fatalf("quoteIdent(%q) = %q, decodes to %q — round-trip lost", name, q, decoded.String())
		}
	})
}
