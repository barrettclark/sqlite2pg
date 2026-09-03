#!/usr/bin/env bash
#
# verify-all-fixtures.sh — full local load-test campaign for sqlite2pg.
#
# For every SQLite database in a list, run the complete pipeline against a
# real local Postgres and use `sqlite2pg verify` (not ad-hoc MD5/row-count
# tricks) as the correctness check:
#
#     profile  ->  mark every column reviewed  ->  load  ->  verify  ->  dropdb
#
# and emit a per-database results table. This is the campaign the
# 2026-08-29 and 2026-09-01 audit cycles ran by hand; checked in so future
# cycles (and a standalone run by a maintainer) don't re-derive it.
#
# Usage:
#     scripts/verify-all-fixtures.sh [DB_PATH ...]
#
# With no arguments, uses the built-in local set: every database under
# testdata/fixtures/, every *.db under $MORE_DATA_DIR, and $BEETS_DB if
# present.
#
# Environment:
#     PG_URL                Postgres URL, NO database name.
#                           Default: postgres://localhost:5432/?sslmode=disable
#     MORE_DATA_DIR         Directory of extra real-world *.db files.
#                           Default: "<repo>/../more data" (the convention
#                           this project's audit cycles have used). Skipped
#                           silently if it doesn't exist.
#     BEETS_DB              Path to the large beets_library.db fixture.
#                           Default: ~/Downloads/beets_library.db. Skipped
#                           silently if absent.
#     SQLITE2PG_BIN           Path to a prebuilt `sqlite2pg`. Default: build it
#                           into ./bin/sqlite2pg from ./cmd/sqlite2pg.
#     WORK_DIR              Scratch dir for configs/reports/state files.
#                           Default: a fresh mktemp -d (removed on exit
#                           unless KEEP_WORK is set).
#     KEEP_WORK             If non-empty, keep WORK_DIR after the run.
#     SAMPLE_SIZE           `sqlite2pg profile --sample-size`. Default: 500.
#     PROFILE_ONLY_OVER_MB  Databases larger than this are profiled only
#                           (no load/verify), to keep a routine campaign
#                           bounded. Default: 1200. Set to 0 to load
#                           everything regardless of size.
#     LOAD_TIMEOUT          `timeout` spec for the load step. Default: 30m.
#     VERIFY_TIMEOUT        `timeout` spec for the verify step. Default: 30m.
#
# Exit status: 0 only if every database that was loaded also verified clean.

set -uo pipefail

# --- configuration -----------------------------------------------------------

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

PG_URL="${PG_URL:-postgres://localhost:5432/?sslmode=disable}"
MORE_DATA_DIR="${MORE_DATA_DIR:-$REPO_ROOT/../more data}"
BEETS_DB="${BEETS_DB:-$HOME/Downloads/beets_library.db}"
SAMPLE_SIZE="${SAMPLE_SIZE:-500}"
PROFILE_ONLY_OVER_MB="${PROFILE_ONLY_OVER_MB:-1200}"
LOAD_TIMEOUT="${LOAD_TIMEOUT:-30m}"
VERIFY_TIMEOUT="${VERIFY_TIMEOUT:-30m}"

# psql / dropdb are not always on PATH (e.g. a stale Postgres.app entry
# shadowing a Homebrew install). Fall back to the Homebrew keg.
if ! command -v dropdb >/dev/null 2>&1; then
    for d in /opt/homebrew/opt/postgresql@*/bin /usr/local/opt/postgresql@*/bin; do
        [ -x "$d/dropdb" ] && PATH="$d:$PATH" && break
    done
fi
if ! command -v dropdb >/dev/null 2>&1; then
    echo "error: dropdb not found on PATH; install libpq/postgresql client tools" >&2
    exit 2
fi

# `timeout` is coreutils; macOS may only have `gtimeout`.
TIMEOUT_BIN="timeout"
command -v timeout >/dev/null 2>&1 || TIMEOUT_BIN="gtimeout"
command -v "$TIMEOUT_BIN" >/dev/null 2>&1 || TIMEOUT_BIN=""   # run without a timeout

run_with_timeout() {
    local spec="$1"; shift
    if [ -n "$TIMEOUT_BIN" ]; then
        "$TIMEOUT_BIN" "$spec" "$@"
    else
        "$@"
    fi
}

# --- sqlite2pg binary --------------------------------------------------------

if [ -n "${SQLITE2PG_BIN:-}" ]; then
    BIN="$SQLITE2PG_BIN"
else
    BIN="$REPO_ROOT/bin/sqlite2pg"
    echo "building sqlite2pg -> $BIN" >&2
    go build -o "$BIN" ./cmd/sqlite2pg || { echo "error: build failed" >&2; exit 2; }
fi

# --- scratch dir ---------------------------------------------------------

if [ -n "${WORK_DIR:-}" ]; then
    mkdir -p "$WORK_DIR"
    CLEAN_WORK=0
else
    WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/verify-all-fixtures.XXXXXX")"
    CLEAN_WORK=1
fi
[ -n "${KEEP_WORK:-}" ] && CLEAN_WORK=0

# Databases this run provisioned, so a Ctrl-C still cleans up.
CREATED_DBS=()
cleanup() {
    for db in "${CREATED_DBS[@]:-}"; do
        [ -n "$db" ] && dropdb --if-exists "$db" 2>/dev/null
    done
    if [ "$CLEAN_WORK" = "1" ]; then rm -rf "$WORK_DIR"; fi
}
trap cleanup EXIT INT TERM

# --- database list -----------------------------------------------------------

DBS=()
if [ "$#" -gt 0 ]; then
    DBS=("$@")
else
    for f in testdata/fixtures/*.db testdata/fixtures/*.sqlite testdata/fixtures/*.geodatabase; do
        [ -e "$f" ] && DBS+=("$f")
    done
    if [ -d "$MORE_DATA_DIR" ]; then
        for f in "$MORE_DATA_DIR"/*.db; do
            [ -e "$f" ] && DBS+=("$f")
        done
    fi
    [ -e "$BEETS_DB" ] && DBS+=("$BEETS_DB")
fi

# --- results table ---------------------------------------------------------

RESULTS_MD="$WORK_DIR/results.md"
{
    echo "# verify-all-fixtures campaign — $(date '+%Y-%m-%d %H:%M:%S')"
    echo
    echo "PG_URL: \`$PG_URL\`  |  sample-size: $SAMPLE_SIZE  |  profile-only over: ${PROFILE_ONLY_OVER_MB}MB"
    echo
    echo "| # | database | size | tables | cols | need-review | profile | load | verify | rows cmp | notes |"
    echo "|---|---|---:|---:|---:|---:|---|---|---|---:|---|"
} >"$RESULTS_MD"

pass_count=0
fail_count=0
error_count=0
idx=0

emit_row() {
    echo "| $* |" >>"$RESULTS_MD"
}

for db in "${DBS[@]}"; do
    idx=$((idx + 1))
    name="$(basename "$db")"

    if [ ! -f "$db" ]; then
        echo ">>> [$idx] $name — MISSING ($db)" >&2
        emit_row "$idx | $name | - | - | - | - | MISSING | - | - | - | file not found"
        error_count=$((error_count + 1))
        continue
    fi

    bytes=$(stat -f%z "$db" 2>/dev/null || stat -c%s "$db" 2>/dev/null || echo 0)
    mb=$((bytes / 1024 / 1024))
    human_size="${mb}MB"; [ "$mb" = "0" ] && human_size="$((bytes / 1024))KB"

    cfg="$WORK_DIR/${idx}_${name}.migration.yaml"
    prof_log="$WORK_DIR/${idx}_${name}.profile.log"
    load_log="$WORK_DIR/${idx}_${name}.load.log"
    verify_report="$WORK_DIR/${idx}_${name}.verify.txt"
    verify_log="$WORK_DIR/${idx}_${name}.verify.log"

    echo ""
    echo ">>> [$idx/${#DBS[@]}] $name ($human_size)"

    # --- profile ---------------------------------------------------------
    # `sqlite2pg profile` exits non-zero when columns need review; that's
    # expected here, we mark them ourselves. What matters is the config
    # got written.
    t0=$SECONDS
    "$BIN" profile --sample-size "$SAMPLE_SIZE" -out "$cfg" "$db" >"$prof_log" 2>&1
    prof_rc=$?
    prof_secs=$((SECONDS - t0))

    if [ ! -f "$cfg" ]; then
        echo "    profile: NO CONFIG (rc=$prof_rc) — see $prof_log"
        emit_row "$idx | $name | $human_size | - | - | - | CRASH (rc=$prof_rc) | - | - | - | $(tail -n1 "$prof_log" | tr '|' '/')"
        error_count=$((error_count + 1))
        continue
    fi

    total_cols=$(grep -cE '^[[:space:]]+reviewed: (true|false)$' "$cfg")
    need_review=$(grep -cE '^[[:space:]]+needs_review: true$' "$cfg")
    n_tables=$(grep -cE '^[[:space:]]{4}[A-Za-z0-9_"]+:$' "$cfg")
    profile_cell="ok ${prof_secs}s"
    [ "$prof_rc" != "0" ] && profile_cell="ok* ${prof_secs}s"   # * = had needs-review

    # --- mark every column reviewed ------------------------------------
    # Simulates a human rubber-stamping every suggestion — the campaign is
    # testing the tool's own auto-picks end to end, and stress-testing
    # `verify` at scale, not human review workflow.
    sed -i.bak -E 's/^([[:space:]]+)reviewed: false$/\1reviewed: true/' "$cfg" && rm -f "$cfg.bak"

    # --- size gate -----------------------------------------------------
    if [ "$PROFILE_ONLY_OVER_MB" != "0" ] && [ "$mb" -gt "$PROFILE_ONLY_OVER_MB" ]; then
        echo "    load/verify SKIPPED — ${mb}MB over ${PROFILE_ONLY_OVER_MB}MB profile-only gate"
        emit_row "$idx | $name | $human_size | $n_tables | $total_cols | $need_review | $profile_cell | skipped (size) | skipped | - | profile-only, ${mb}MB"
        continue
    fi

    # --- load --------------------------------------------------------
    t0=$SECONDS
    run_with_timeout "$LOAD_TIMEOUT" "$BIN" load --noverify --pg "$PG_URL" "$cfg" >"$load_log" 2>&1 </dev/null
    load_rc=$?
    load_secs=$((SECONDS - t0))

    statef="$cfg.state.json"
    created_db=""
    [ -f "$statef" ] && created_db=$(sed -n 's/.*"database"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$statef")
    [ -n "$created_db" ] && CREATED_DBS+=("$created_db")

    if [ "$load_rc" != "0" ]; then
        echo "    load: FAIL (rc=$load_rc, ${load_secs}s) — see $load_log"
        emit_row "$idx | $name | $human_size | $n_tables | $total_cols | $need_review | $profile_cell | FAIL rc=$load_rc ${load_secs}s | - | - | $(tail -n1 "$load_log" | tr '|' '/' | cut -c1-140)"
        error_count=$((error_count + 1))
        [ -n "$created_db" ] && dropdb --if-exists "$created_db" 2>/dev/null
        continue
    fi
    echo "    load: ok (${load_secs}s) -> db $created_db"

    # --- verify ----------------------------------------------------
    t0=$SECONDS
    run_with_timeout "$VERIFY_TIMEOUT" "$BIN" verify --pg "$PG_URL" --out "$verify_report" "$db" "$cfg" >"$verify_log" 2>&1 </dev/null
    verify_rc=$?
    verify_secs=$((SECONDS - t0))

    rows_cmp=$(sed -n 's/.*checked, \([0-9]*\) row(s) compared.*/\1/p' "$verify_log" | head -n1)
    [ -z "$rows_cmp" ] && rows_cmp="?"

    if [ "$verify_rc" = "0" ]; then
        echo "    verify: PASS (${verify_secs}s, $rows_cmp rows)"
        emit_row "$idx | $name | $human_size | $n_tables | $total_cols | $need_review | $profile_cell | ok ${load_secs}s | PASS ${verify_secs}s | $rows_cmp | -"
        pass_count=$((pass_count + 1))
    else
        note=$(grep -m1 -hE '(MISMATCH|verification FAILED|row-count)' "$verify_log" "$verify_report" 2>/dev/null | head -n1 | cut -c1-160)
        [ -z "$note" ] && note=$(tail -n1 "$verify_log" | cut -c1-140)
        echo "    verify: FAIL (rc=$verify_rc, ${verify_secs}s) — $note"
        emit_row "$idx | $name | $human_size | $n_tables | $total_cols | $need_review | $profile_cell | ok ${load_secs}s | FAIL ${verify_secs}s | $rows_cmp | $(echo "$note" | tr '|' '/')"
        fail_count=$((fail_count + 1))
    fi

    # --- dropdb --------------------------------------------------
    if [ -n "$created_db" ]; then
        dropdb --if-exists "$created_db" 2>/dev/null && echo "    dropped $created_db"
        # remove from CREATED_DBS so the EXIT trap doesn't retry
        for i in "${!CREATED_DBS[@]}"; do
            [ "${CREATED_DBS[$i]}" = "$created_db" ] && unset 'CREATED_DBS[$i]'
        done
    fi
done

# --- summary -----------------------------------------------------------

{
    echo
    echo "## Summary"
    echo
    echo "- verified clean: **$pass_count**"
    echo "- verify FAILED: **$fail_count**"
    echo "- profile/load errors or skips: **$error_count**"
    echo
    echo "Work dir: \`$WORK_DIR\` (logs + per-db verify reports)"
} >>"$RESULTS_MD"

echo
echo "=========================================================="
cat "$RESULTS_MD"
echo "=========================================================="
echo "results table: $RESULTS_MD"

[ "$fail_count" = "0" ] && [ "$error_count" = "0" ]
