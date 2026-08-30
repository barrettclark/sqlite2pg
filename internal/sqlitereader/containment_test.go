package sqlitereader

import "testing"

func TestColumnValuesContainedIn_TrueWhenEveryNonNullValueExistsInTheReferencedColumn(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE customers (id INTEGER PRIMARY KEY);
		CREATE TABLE invoices (id INTEGER PRIMARY KEY, customer_id INTEGER);
	`)
	db.Exec(`INSERT INTO customers (id) VALUES (1), (2), (3)`)
	db.Exec(`INSERT INTO invoices (customer_id) VALUES (1), (2), (2)`)

	contained, nonNullCount, err := ColumnValuesContainedIn(db, "invoices", "customer_id", "customers", "id")
	if err != nil {
		t.Fatalf("ColumnValuesContainedIn: %v", err)
	}
	if !contained {
		t.Error("expected every non-null value to be contained")
	}
	if nonNullCount != 3 {
		t.Errorf("expected 3 non-null values, got %d", nonNullCount)
	}
}

func TestColumnValuesContainedIn_FalseWhenAValueIsMissing(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE customers (id INTEGER PRIMARY KEY);
		CREATE TABLE invoices (id INTEGER PRIMARY KEY, customer_id INTEGER);
	`)
	db.Exec(`INSERT INTO customers (id) VALUES (1), (2)`)
	db.Exec(`INSERT INTO invoices (customer_id) VALUES (1), (99)`)

	contained, _, err := ColumnValuesContainedIn(db, "invoices", "customer_id", "customers", "id")
	if err != nil {
		t.Fatalf("ColumnValuesContainedIn: %v", err)
	}
	if contained {
		t.Error("expected containment to fail when a value (99) doesn't exist in the referenced column")
	}
}

func TestColumnValuesContainedIn_IgnoresNulls(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE customers (id INTEGER PRIMARY KEY);
		CREATE TABLE invoices (id INTEGER PRIMARY KEY, customer_id INTEGER);
	`)
	db.Exec(`INSERT INTO customers (id) VALUES (1)`)
	db.Exec(`INSERT INTO invoices (customer_id) VALUES (1), (NULL)`)

	contained, nonNullCount, err := ColumnValuesContainedIn(db, "invoices", "customer_id", "customers", "id")
	if err != nil {
		t.Fatalf("ColumnValuesContainedIn: %v", err)
	}
	if !contained {
		t.Error("expected NULL to be ignored, not treated as a missing value")
	}
	if nonNullCount != 1 {
		t.Errorf("expected 1 non-null value, got %d", nonNullCount)
	}
}

func TestColumnValuesContainedIn_FalseWhenColumnIsEntirelyNull(t *testing.T) {
	// An all-NULL column has no real values to check, so it's not
	// meaningful evidence of a relationship — treated as not contained
	// (via a zero non-null count) so callers don't suggest a foreign key
	// backed by no actual data.
	db := openTestDB(t, `
		CREATE TABLE customers (id INTEGER PRIMARY KEY);
		CREATE TABLE invoices (id INTEGER PRIMARY KEY, customer_id INTEGER);
	`)
	db.Exec(`INSERT INTO customers (id) VALUES (1)`)
	db.Exec(`INSERT INTO invoices (customer_id) VALUES (NULL)`)

	contained, nonNullCount, err := ColumnValuesContainedIn(db, "invoices", "customer_id", "customers", "id")
	if err != nil {
		t.Fatalf("ColumnValuesContainedIn: %v", err)
	}
	if contained {
		t.Error("expected an all-NULL column not to be reported as contained")
	}
	if nonNullCount != 0 {
		t.Errorf("expected 0 non-null values, got %d", nonNullCount)
	}
}
