package pipeline

import (
	"testing"

	_ "modernc.org/sqlite"

	"sqlite2pg/internal/sqlitereader"
)

func TestInferForeignKeys_SuggestsAColumnMatchingNamingConventionAndContainment(t *testing.T) {
	db, _ := openTestDB(t, `
		CREATE TABLE Customers (CustomerId INTEGER PRIMARY KEY);
		CREATE TABLE Invoices (InvoiceId INTEGER PRIMARY KEY, CustomerId INTEGER);
	`)
	db.Exec(`INSERT INTO Customers (CustomerId) VALUES (1), (2)`)
	db.Exec(`INSERT INTO Invoices (CustomerId) VALUES (1), (2), (1)`)

	tables, _, err := sqlitereader.ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}

	got, err := inferForeignKeys(db, tables)
	if err != nil {
		t.Fatalf("inferForeignKeys: %v", err)
	}

	suggestions := got["Invoices"]
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 suggestion for Invoices, got %d: %+v", len(suggestions), suggestions)
	}
	sfk := suggestions[0]
	if len(sfk.Columns) != 1 || sfk.Columns[0] != "CustomerId" {
		t.Errorf("expected local column CustomerId, got %v", sfk.Columns)
	}
	if sfk.RefTable != "Customers" {
		t.Errorf("expected ref table Customers, got %q", sfk.RefTable)
	}
	if len(sfk.RefColumns) != 1 || sfk.RefColumns[0] != "CustomerId" {
		t.Errorf("expected ref column CustomerId, got %v", sfk.RefColumns)
	}
	if sfk.Rationale == "" {
		t.Error("expected a non-empty rationale")
	}
}

func TestInferForeignKeys_MatchesSimplePluralTableNames(t *testing.T) {
	db, _ := openTestDB(t, `
		CREATE TABLE genre (genre_id INTEGER PRIMARY KEY);
		CREATE TABLE tracks (track_id INTEGER PRIMARY KEY, genre_id INTEGER);
	`)
	db.Exec(`INSERT INTO genre (genre_id) VALUES (1)`)
	db.Exec(`INSERT INTO tracks (genre_id) VALUES (1)`)

	tables, _, err := sqlitereader.ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}

	got, err := inferForeignKeys(db, tables)
	if err != nil {
		t.Fatalf("inferForeignKeys: %v", err)
	}
	if len(got["tracks"]) != 1 {
		t.Fatalf("expected 1 suggestion for tracks, got %d: %+v", len(got["tracks"]), got["tracks"])
	}
}

func TestInferForeignKeys_SkipsColumnsAlreadyCoveredByADeclaredForeignKey(t *testing.T) {
	db, _ := openTestDB(t, `
		CREATE TABLE Customers (CustomerId INTEGER PRIMARY KEY);
		CREATE TABLE Invoices (
			InvoiceId INTEGER PRIMARY KEY,
			CustomerId INTEGER,
			FOREIGN KEY (CustomerId) REFERENCES Customers (CustomerId)
		);
	`)
	db.Exec(`INSERT INTO Customers (CustomerId) VALUES (1)`)
	db.Exec(`INSERT INTO Invoices (CustomerId) VALUES (1)`)

	tables, _, err := sqlitereader.ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}

	got, err := inferForeignKeys(db, tables)
	if err != nil {
		t.Fatalf("inferForeignKeys: %v", err)
	}
	if len(got["Invoices"]) != 0 {
		t.Errorf("expected no suggestions for an already-declared foreign key, got %+v", got["Invoices"])
	}
}

func TestInferForeignKeys_SkipsWhenValuesAreNotContained(t *testing.T) {
	db, _ := openTestDB(t, `
		CREATE TABLE Customers (CustomerId INTEGER PRIMARY KEY);
		CREATE TABLE Invoices (InvoiceId INTEGER PRIMARY KEY, CustomerId INTEGER);
	`)
	db.Exec(`INSERT INTO Customers (CustomerId) VALUES (1)`)
	db.Exec(`INSERT INTO Invoices (CustomerId) VALUES (1), (99)`)

	tables, _, err := sqlitereader.ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}

	got, err := inferForeignKeys(db, tables)
	if err != nil {
		t.Fatalf("inferForeignKeys: %v", err)
	}
	if len(got["Invoices"]) != 0 {
		t.Errorf("expected no suggestion when a value (99) isn't contained, got %+v", got["Invoices"])
	}
}

func TestInferForeignKeys_SkipsWhenNoTableNameMatchesTheNamingConvention(t *testing.T) {
	db, _ := openTestDB(t, `
		CREATE TABLE widgets (widget_id INTEGER PRIMARY KEY, status_id INTEGER);
	`)
	db.Exec(`INSERT INTO widgets (status_id) VALUES (1)`)

	tables, _, err := sqlitereader.ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}

	got, err := inferForeignKeys(db, tables)
	if err != nil {
		t.Fatalf("inferForeignKeys: %v", err)
	}
	if len(got["widgets"]) != 0 {
		t.Errorf("expected no suggestion when no table named status/statuses exists, got %+v", got["widgets"])
	}
}

func TestInferForeignKeys_SkipsWhenReferencedTableHasNoSingleColumnPrimaryKey(t *testing.T) {
	db, _ := openTestDB(t, `
		CREATE TABLE Customers (a INTEGER, b INTEGER, PRIMARY KEY (a, b));
		CREATE TABLE Invoices (InvoiceId INTEGER PRIMARY KEY, CustomerId INTEGER);
	`)
	db.Exec(`INSERT INTO Invoices (CustomerId) VALUES (1)`)

	tables, _, err := sqlitereader.ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}

	got, err := inferForeignKeys(db, tables)
	if err != nil {
		t.Fatalf("inferForeignKeys: %v", err)
	}
	if len(got["Invoices"]) != 0 {
		t.Errorf("expected no suggestion when Customers has a composite primary key, got %+v", got["Invoices"])
	}
}
