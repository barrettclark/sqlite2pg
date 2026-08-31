# Fixture provenance

The classic sample and real-world databases in this directory came from:

- https://www.timestored.com/data/sample/sqlite
- https://www.timestored.com/data/sample/csv (CSV files were imported into SQLite)
- https://sqlite.org/test-dbs/dir?ci=tip
- https://github.com/codecrafters-io/sample-sqlite-databases
- https://catalog.data.gov/

The `sample-*.sqlite` files (`sample-types`, `sample-dates`, `sample-large`,
`sample-numeric-text`, `sample-uuids`, `sample-varchar`,
`sample-type-mismatch`, `sample-implicit-fk`) are small handcrafted fixtures
authored for this project, not sourced externally — each targets one
specific data shape the test suite needs coverage for and is documented at
its point of use in `internal/pipeline/golden_test.go`.

Additional databases from these same sources are used locally for testing
and validating changes but aren't checked into this repo.
