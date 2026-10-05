package main

import "testing"

func TestCheckIdentityRange(t *testing.T) {
	tests := []struct {
		name      string
		typ       string
		highWater int64
		wantErr   bool
	}{
		{name: "integer at max", typ: "integer", highWater: 2147483647, wantErr: false},
		{name: "integer above max", typ: "integer", highWater: 2147483648, wantErr: true},
		{name: "upper-case INTEGER above max", typ: "INTEGER", highWater: 2147483648, wantErr: true},
		{name: "smallint above max", typ: "smallint", highWater: 32768, wantErr: true},
		{name: "bigint has no overflow below int64 max", typ: "bigint", highWater: 1 << 40, wantErr: false},
		{name: "unknown type is not range-checked", typ: "text", highWater: 1 << 40, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkIdentityRange("t", "id", tt.typ, tt.highWater)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkIdentityRange(%q, %d) error = %v, wantErr %v", tt.typ, tt.highWater, err, tt.wantErr)
			}
		})
	}
}
